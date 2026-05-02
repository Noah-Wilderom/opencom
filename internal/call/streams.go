package call

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	"opencom/internal/transport/p2p"
)

// ProtocolID is the libp2p stream protocol for call control messages.
const ProtocolID protocol.ID = "/opencom/control/1.0.0"

// Resolver looks up current addresses for a peer ID. The Engine
// consults a Resolver (if set) before dialing, so peers that aren't
// already in the libp2p peerstore can still be reached via discovery.
type Resolver interface {
	Resolve(ctx context.Context, target peer.ID) ([]ma.Multiaddr, error)
	InvalidateCache(target peer.ID)
}

// Engine wires the libp2p host to the call manager: it registers the
// /opencom/control/1.0.0 stream handler for inbound calls, dials peers
// for outbound calls, and drives sessions to/from each transition by
// reading and writing JSON Messages.
type Engine struct {
	host    *p2p.Host
	manager *Manager
	log     *zap.Logger
	now     func() time.Time

	mu      sync.Mutex
	streams map[string]network.Stream // session ID → stream
	encs    map[string]*json.Encoder

	resolver Resolver
	relays   []peer.AddrInfo // for circuit-relay fallback in Place
}

// SetResolver installs r so subsequent Place calls consult it before
// dialing. Safe to call once at daemon startup; not thread-safe to
// call concurrently with Place.
func (e *Engine) SetResolver(r Resolver) { e.resolver = r }

// SetRelays installs the configured circuit-relay peers. Place uses
// them to populate the peerstore with /<relay>/p2p-circuit fallback
// addresses for the target so libp2p has a guaranteed try-via-relay
// path even when the resolver/peerstore has no fresh direct addrs
// (the dominant cross-network failure mode while opencom's DHT layer
// is still flaky). Safe to call once at daemon startup; not
// thread-safe to call concurrently with Place.
func (e *Engine) SetRelays(relays []peer.AddrInfo) { e.relays = relays }

// NewEngine constructs an Engine ready to be Start()ed.
func NewEngine(h *p2p.Host, m *Manager, log *zap.Logger, now func() time.Time) *Engine {
	if log == nil {
		log = zap.NewNop()
	}
	if now == nil {
		now = time.Now
	}
	return &Engine{
		host:    h,
		manager: m,
		log:     log,
		now:     now,
		streams: make(map[string]network.Stream),
		encs:    make(map[string]*json.Encoder),
	}
}

// Start registers the stream handler. Call once at daemon startup.
func (e *Engine) Start() {
	e.host.HostInternal().SetStreamHandler(ProtocolID, e.handleStream)
}

// Stop removes the stream handler and closes all currently-open call
// streams. After Stop, no new inbound calls will be accepted; outbound
// Place calls return errors. Safe to call multiple times.
func (e *Engine) Stop() {
	e.host.HostInternal().RemoveStreamHandler(ProtocolID)

	e.mu.Lock()
	streams := make([]network.Stream, 0, len(e.streams))
	for _, s := range e.streams {
		streams = append(streams, s)
	}
	// Clear maps under lock so concurrent write() calls fail fast.
	e.streams = make(map[string]network.Stream)
	e.encs = make(map[string]*json.Encoder)
	e.mu.Unlock()

	for _, s := range streams {
		_ = s.Close()
	}
}

// Place dials remote, opens a fresh control stream, sends INVITE, registers
// the new Outbound Session, and starts the read loop. Returns the Session
// in StateRinging.
//
// Dial strategy:
//  1. Consult Resolver (cache → DHT) for current addresses; merge into peerstore.
//  2. Try NewStream.
//  3. If NewStream fails (commonly: peer moved networks, relay reservation
//     expired, peerstore has only stale entries from a prior session), force
//     a fresh DHT lookup by invalidating the resolver cache, repopulate the
//     peerstore, and retry NewStream once.
//
// One bounded retry — not a loop — keeps Place latency predictable while
// catching the common stale-address case without making the user re-issue
// `opencom call`.
func (e *Engine) Place(ctx context.Context, remote peer.ID) (*Session, error) {
	e.populatePeerstoreFromResolver(ctx, remote, false)
	e.populatePeerstoreWithRelayFallback(remote)
	// Allow opening the call control stream over a libp2p "limited"
	// (relay-v2) connection. Without this opt-in, libp2p's swarm
	// refuses to open streams on relayed connections and blocks
	// waitForDirectConn until DCUtR succeeds — which is exactly the
	// "failed to open stream: context deadline exceeded" symptom we
	// kept hitting cross-network. Audio media still requires datagrams
	// (direct QUIC), but the JSON control plane is happy over relay.
	dialCtx := network.WithAllowLimitedConn(ctx, "opencom-call-control")
	stream, err := e.host.HostInternal().NewStream(dialCtx, remote, ProtocolID)
	if err != nil && e.resolver != nil {
		e.log.Debug("first dial failed; forcing fresh DHT lookup and retrying",
			zap.String("peer", remote.String()), zap.Error(err))
		if e.populatePeerstoreFromResolver(ctx, remote, true) {
			stream, err = e.host.HostInternal().NewStream(dialCtx, remote, ProtocolID)
		}
	}
	if err != nil {
		if e.resolver != nil {
			e.resolver.InvalidateCache(remote)
		}
		return nil, fmt.Errorf("opening control stream to %s: %w", remote, translateDialError(remote, err))
	}

	id := NewCallID()
	s := NewSession(id, remote, Outbound, e.now)
	e.bind(s, stream)
	e.manager.Register(s)

	if err := e.write(s, Message{
		Type:   MsgInvite,
		CallID: id,
		Caller: e.host.ID().String(),
	}); err != nil {
		_ = s.End("failed to send INVITE: " + err.Error())
		e.unbindAndClose(s.ID())
		e.manager.Remove(s.ID())
		return nil, fmt.Errorf("sending INVITE: %w", err)
	}

	if err := s.ToRinging(); err != nil {
		return nil, fmt.Errorf("transitioning to Ringing: %w", err)
	}
	go e.readLoop(s)
	return s, nil
}

// translateDialError rewrites libp2p's verbose dial-failure errors
// into something a user can act on. The single most common opaque
// failure mode users hit is a relay-mediated dial whose every
// candidate path returns NO_RESERVATION (the target peer hasn't
// reserved a circuit slot, or the relay restarted and dropped its
// reservation table). Rather than leaving them to grep a 26-line wall
// of dial errors, we surface that explicitly.
//
// On any non-matching error we return it unchanged so callers still
// get the full libp2p detail when something genuinely novel breaks.
func translateDialError(remote peer.ID, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if !strings.Contains(msg, "NO_RESERVATION") {
		return err
	}
	return fmt.Errorf("peer %s has no relay reservation — they may need to "+
		"restart their daemon or their network is blocking AutoRelay "+
		"(original: %w)", remote, err)
}

// populatePeerstoreWithRelayFallback adds /<relay>/p2p-circuit dial
// candidates for target into the peerstore on every Place call.
//
// We add unconditionally — even when the peerstore already has some
// addresses for the target — because the persisted peerstore on disk
// commonly holds stale entries from previous sessions (LAN addresses
// from a different network, expired QUIC ports, etc.) that libp2p
// will happily try and time out on. Without the relay path also being
// in the dial set, NewStream sits on those stale addrs until ctx
// cancels. With it, libp2p's parallel dialer races the relay path
// against the stale ones and the relay typically wins.
//
// Safe to call repeatedly: AddAddr is additive and TempAddrTTL keeps
// the peerstore from growing unboundedly.
//
// No-op when no relays are configured (test rigs that pass empty
// HostRelays, custom deployments).
func (e *Engine) populatePeerstoreWithRelayFallback(target peer.ID) {
	if len(e.relays) == 0 {
		return
	}
	pstore := e.host.HostInternal().Peerstore()
	circuit, err := ma.NewMultiaddr("/p2p-circuit")
	if err != nil {
		return
	}
	for _, relay := range e.relays {
		relayP2P, err := ma.NewMultiaddr("/p2p/" + relay.ID.String())
		if err != nil {
			continue
		}
		for _, addr := range relay.Addrs {
			full := addr.Encapsulate(relayP2P).Encapsulate(circuit)
			pstore.AddAddr(target, full, peerstore.TempAddrTTL)
		}
	}
}

// populatePeerstoreFromResolver consults the Resolver and merges any
// returned addresses into the libp2p peerstore. Returns true iff the
// peerstore actually gained addresses (i.e. the Resolver succeeded and
// returned a non-empty list).
//
// When forceFresh is true, the resolver's disk cache is invalidated
// first so the lookup goes to the DHT — used by Place's retry path
// after a stale-address dial failure. forceFresh is a no-op when no
// Resolver is configured.
func (e *Engine) populatePeerstoreFromResolver(ctx context.Context, remote peer.ID, forceFresh bool) bool {
	if e.resolver == nil {
		return false
	}
	if forceFresh {
		e.resolver.InvalidateCache(remote)
	}
	addrs, err := e.resolver.Resolve(ctx, remote)
	if err != nil || len(addrs) == 0 {
		if err != nil {
			e.log.Debug("resolver lookup failed",
				zap.String("peer", remote.String()),
				zap.Bool("force_fresh", forceFresh),
				zap.Error(err))
		}
		return false
	}
	e.host.HostInternal().Peerstore().AddAddrs(remote, addrs, peerstore.TempAddrTTL)
	return true
}

// Accept sends ACCEPT for an inbound Session, advancing it to Connecting
// then Connected. The remote peer's read loop sees the ACCEPT and mirrors
// the transition.
func (e *Engine) Accept(s *Session) error {
	if s.Direction() != Inbound {
		return fmt.Errorf("session %s is not inbound", s.ID())
	}
	if err := e.write(s, Message{Type: MsgAccept, CallID: s.ID()}); err != nil {
		return fmt.Errorf("sending ACCEPT: %w", err)
	}
	if err := s.ToConnecting(); err != nil {
		return fmt.Errorf("Connecting: %w", err)
	}
	if err := s.ToConnected(); err != nil {
		return fmt.Errorf("Connected: %w", err)
	}
	return nil
}

// Hangup sends HANGUP and transitions the local Session to Ended. The
// remote peer's read loop sees the HANGUP and mirrors. Closes the stream.
func (e *Engine) Hangup(s *Session, reason string) error {
	if err := e.write(s, Message{Type: MsgHangup, CallID: s.ID(), Reason: reason}); err != nil {
		e.log.Debug("hangup write failed", zap.String("session", s.ID()), zap.Error(err))
	}
	_ = s.End(reason)
	e.unbindAndClose(s.ID())
	e.manager.Remove(s.ID())
	return nil
}

// handleStream is the libp2p stream handler. The first message must be an
// INVITE; we register an Inbound Session, then run the read loop.
func (e *Engine) handleStream(stream network.Stream) {
	remote := stream.Conn().RemotePeer()

	// Bounded read of the first message with a deadline to prevent a
	// peer from parking a goroutine here by opening the stream and
	// never sending bytes.
	_ = stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 4*1024), 1<<20)
	if !scanner.Scan() {
		_ = stream.Reset()
		return
	}
	var first Message
	if err := json.Unmarshal(scanner.Bytes(), &first); err != nil {
		e.log.Debug("malformed first message", zap.Error(err))
		_ = stream.Reset()
		return
	}
	if first.Type != MsgInvite || first.CallID == "" {
		e.log.Debug("first message is not a valid INVITE", zap.Any("msg", first))
		_ = stream.Reset()
		return
	}
	// Validate Caller (defense in depth — libp2p already authenticates RemotePeer).
	if first.Caller != "" && first.Caller != remote.String() {
		e.log.Debug("INVITE caller mismatch",
			zap.String("claimed", first.Caller),
			zap.String("actual", remote.String()))
		_ = stream.Reset()
		return
	}
	// Clear the read deadline now that we trust the stream.
	_ = stream.SetReadDeadline(time.Time{})

	s := NewSession(first.CallID, remote, Inbound, e.now)
	e.bind(s, stream)
	// Register BEFORE the Ringing transition so the Manager's
	// per-session forwarder is in place before the first state-change
	// event fires. Otherwise WatchCalls (and any other Manager-level
	// subscriber) misses the inbound "ringing" event entirely — the
	// reason desktop notifications weren't firing for incoming calls.
	e.manager.Register(s)
	if err := s.ToRinging(); err != nil {
		_ = stream.Close()
		return
	}
	e.runScanner(s, scanner)
}

// readLoop owns the outbound stream's read side. It blocks on the next
// message and dispatches it.
func (e *Engine) readLoop(s *Session) {
	stream := e.streamFor(s.ID())
	if stream == nil {
		return
	}
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 4*1024), 1<<20)
	e.runScanner(s, scanner)
}

func (e *Engine) runScanner(s *Session, scanner *bufio.Scanner) {
	defer func() {
		e.unbindAndClose(s.ID())
		e.manager.Remove(s.ID())
	}()
	for scanner.Scan() {
		var m Message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			e.log.Debug("malformed message", zap.Error(err))
			continue
		}
		if m.CallID != s.ID() {
			e.log.Debug("call id mismatch", zap.String("expected", s.ID()), zap.String("got", m.CallID))
			continue
		}
		switch m.Type {
		case MsgAccept:
			_ = s.ToConnecting()
			_ = s.ToConnected()
		case MsgDecline:
			_ = s.End("declined: " + m.Reason)
			return
		case MsgHangup:
			_ = s.End(m.Reason)
			return
		case MsgInvite:
			// Unexpected on an established session; ignore.
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		_ = s.End("stream error: " + err.Error())
	} else {
		_ = s.End("remote disconnected")
	}
}

// bind associates a stream with a session so write() can find it.
func (e *Engine) bind(s *Session, stream network.Stream) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.streams[s.ID()] = stream
	e.encs[s.ID()] = json.NewEncoder(stream)
}

func (e *Engine) streamFor(id string) network.Stream {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.streams[id]
}

func (e *Engine) unbindAndClose(id string) {
	e.mu.Lock()
	stream := e.streams[id]
	delete(e.streams, id)
	delete(e.encs, id)
	e.mu.Unlock()
	if stream != nil {
		_ = stream.Close()
	}
}

func (e *Engine) write(s *Session, m Message) error {
	e.mu.Lock()
	enc, ok := e.encs[s.ID()]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("no stream for session %s", s.ID())
	}
	return enc.Encode(&m)
}

// NewCallID returns a fresh UUID-v4-based call identifier.
func NewCallID() string { return "c-" + uuid.NewString() }
