package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Client is a JSON-RPC 2.0 client over newline-delimited JSON. Safe for
// concurrent Call invocations.
type Client struct {
	conn          net.Conn
	enc           *json.Encoder
	daemonVersion string

	writeMu sync.Mutex

	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan *Message
	closed  chan struct{}

	// subsMu guards subs and pendEvs.
	subsMu  sync.Mutex
	subs    map[string]chan *Event
	pendEvs map[string][]*Event // events received before Subscribe registered the channel; bounded by maxPendingEventsPerSub
}

// maxPendingEventsPerSub bounds per-subscription pre-registration buffering
// so a misbehaving server (or events for a sub never registered) cannot
// grow pendEvs without bound. Matches the registered-channel buffer cap.
const maxPendingEventsPerSub = 16

// Dial connects to the IPC endpoint at path and performs the protocol
// handshake. The transport is platform-correct (Unix-domain socket on
// Linux/macOS; Windows named pipe on Windows). Errors if the server's
// hello version does not match ProtocolVersion.
func Dial(ctx context.Context, path string) (*Client, error) {
	conn, err := dialTransport(ctx, path)
	if err != nil {
		return nil, err
	}

	c := &Client{
		conn:    conn,
		enc:     json.NewEncoder(conn),
		pending: make(map[int64]chan *Message),
		closed:  make(chan struct{}),
		subs:    make(map[string]chan *Event),
		pendEvs: make(map[string][]*Event),
	}

	helloDeadline := time.Now().Add(5 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(helloDeadline) {
		helloDeadline = dl
	}
	_ = conn.SetReadDeadline(helloDeadline)

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	if !scanner.Scan() {
		conn.Close()
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("reading hello: %w", err)
		}
		return nil, errors.New("daemon closed connection before hello")
	}

	var hello Message
	if err := json.Unmarshal(scanner.Bytes(), &hello); err != nil {
		conn.Close()
		return nil, fmt.Errorf("parsing hello: %w", err)
	}
	if hello.Method != "hello" {
		conn.Close()
		return nil, fmt.Errorf("expected hello, got %q", hello.Method)
	}
	var hp HelloParams
	if err := json.Unmarshal(hello.Params, &hp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("parsing hello params: %w", err)
	}
	if hp.Version != ProtocolVersion {
		conn.Close()
		return nil, fmt.Errorf("daemon protocol mismatch: got %q, want %q (restart `opencom daemon`)",
			hp.Version, ProtocolVersion)
	}
	c.daemonVersion = hp.DaemonVersion

	_ = conn.SetReadDeadline(time.Time{})

	go c.readLoop(scanner)

	return c, nil
}

func (c *Client) readLoop(scanner *bufio.Scanner) {
	defer close(c.closed)
	for scanner.Scan() {
		var msg Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.IsResponse() {
			// Delete-then-send: removing the entry before sending guarantees
			// that neither Call's ctx-done branch nor the tear-down loop
			// below can observe a stale entry. The buffered channel (cap 1)
			// absorbs the send even if Call has already abandoned ch.
			c.mu.Lock()
			ch, ok := c.pending[*msg.ID]
			if ok {
				delete(c.pending, *msg.ID)
			}
			c.mu.Unlock()
			if ok {
				ch <- &msg
			}
			continue
		}
		if msg.IsNotification() && msg.Method == "event" {
			var ev Event
			if err := json.Unmarshal(msg.Params, &ev); err != nil {
				continue
			}
			// Hold subsMu for the entire lookup-and-send so Subscription.Close
			// can't race-close ch between us reading it from the map and
			// sending on it. The send is non-blocking (select/default) so
			// holding the lock briefly cannot deadlock — even if the
			// subscriber has stopped reading, the default branch fires
			// immediately and we release the lock.
			c.subsMu.Lock()
			if ch, ok := c.subs[ev.Sub]; ok {
				select {
				case ch <- &ev:
				default:
					// Drop on overflow rather than block the read loop.
					// TODO(M4): consider per-subscription overflow policy for
					// streams that need lossless delivery (e.g. chat, transfers).
				}
			} else {
				// Subscribe response and emitted events race on the wire;
				// stash the event so a Subscribe call still in flight can
				// drain it once it registers. Bounded by maxPendingEventsPerSub
				// to prevent unbounded growth from misbehaving servers or
				// events for subs that are never registered.
				if c.pendEvs != nil && len(c.pendEvs[ev.Sub]) < maxPendingEventsPerSub {
					c.pendEvs[ev.Sub] = append(c.pendEvs[ev.Sub], &ev)
				}
			}
			c.subsMu.Unlock()
		}
	}
	c.mu.Lock()
	for _, ch := range c.pending {
		close(ch) // signal in-flight Call
	}
	c.pending = nil
	c.mu.Unlock()

	c.subsMu.Lock()
	for _, ch := range c.subs {
		close(ch)
	}
	c.subs = nil
	c.pendEvs = nil
	c.subsMu.Unlock()
}

// Event is a single notification routed to a subscription.
type Event struct {
	Sub  string          `json:"sub"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// Subscription represents a client-side subscription to server events.
// Close stops local routing for this subscription. Server-side cleanup
// happens implicitly on connection close.
type Subscription struct {
	ID     string
	Events <-chan *Event

	client *Client
}

// Close stops local routing for the subscription. Idempotent.
func (s *Subscription) Close() {
	if s == nil || s.client == nil {
		return
	}
	s.client.subsMu.Lock()
	defer s.client.subsMu.Unlock()
	if s.client.subs != nil {
		if ch, ok := s.client.subs[s.ID]; ok {
			delete(s.client.subs, s.ID)
			close(ch)
		}
	}
	s.client = nil
}

// Subscribe calls method with params, expects a response of shape
// {"subscription_id": "..."}, and returns a Subscription whose Events
// channel receives notifications addressed to that subscription ID.
func (c *Client) Subscribe(ctx context.Context, method string, params interface{}) (*Subscription, error) {
	var resp struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if err := c.Call(ctx, method, params, &resp); err != nil {
		return nil, err
	}
	if resp.SubscriptionID == "" {
		return nil, fmt.Errorf("subscribe %s: server response missing subscription_id", method)
	}

	events := make(chan *Event, 16)
	c.subsMu.Lock()
	if c.subs == nil {
		c.subsMu.Unlock()
		return nil, errors.New("client closed")
	}
	c.subs[resp.SubscriptionID] = events
	if buffered, ok := c.pendEvs[resp.SubscriptionID]; ok {
		for _, ev := range buffered {
			select {
			case events <- ev:
			default:
				// Drop on overflow rather than block.
			}
		}
		delete(c.pendEvs, resp.SubscriptionID)
	}
	c.subsMu.Unlock()

	return &Subscription{
		ID:     resp.SubscriptionID,
		Events: events,
		client: c,
	}, nil
}

// DaemonVersion is the build-version string the daemon advertised in its hello.
func (c *Client) DaemonVersion() string { return c.daemonVersion }

// Call invokes a method on the daemon and unmarshals the JSON response into
// result. Pass nil for result if no value is expected.
func (c *Client) Call(ctx context.Context, method string, params, result interface{}) error {
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshalling params: %w", err)
		}
		paramsRaw = b
	}

	id := c.nextID.Add(1)
	ch := make(chan *Message, 1)

	c.mu.Lock()
	if c.pending == nil {
		c.mu.Unlock()
		return errors.New("client closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	c.writeMu.Lock()
	err := c.enc.Encode(&Message{JSONRPC: JSONRPCVersion, ID: &id, Method: method, Params: paramsRaw})
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("sending: %w", err)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.closed:
		return errors.New("client closed during call")
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return errors.New("connection closed")
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

// Close shuts the client down. In-flight Call invocations receive an error
// via c.closed once the read loop observes the broken connection. Close
// itself does not block on the read loop exiting.
func (c *Client) Close() error {
	return c.conn.Close()
}
