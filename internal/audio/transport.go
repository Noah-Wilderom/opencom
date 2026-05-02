package audio

// transport.go — Task 7: datagram channel + audio control stream
//
// Architecture: Option B (dual streams).
//   - Each side opens its own outgoing control stream to the peer.
//   - Each side registers a SetStreamHandler for AudioControlProtocol that
//     receives the peer's outgoing stream and routes messages to Control().
//   - Datagrams use the underlying *quic.Conn reached via network.Conn.As.
//
// Concurrency note: NewLibp2pTransport does NOT block waiting for the peer's
// reciprocal stream. Instead, the readControlLoop goroutine waits for the
// inbound stream to arrive in the per-(host,peer) channel. This prevents a
// deadlock when both sides call NewLibp2pTransport sequentially in a test:
// A opens stream to B → B's handler fires → B's inCh gets A's stream; then B
// opens stream to A → A's handler fires → A's inCh gets B's stream. Both
// readControlLoops unblock immediately.
//
// Public surface required by Tasks 8/9:
//   - Transport interface
//   - NewLibp2pTransport
//   - RegisterStreamHandler   (called once per host at startup / test setup)
//   - ErrDatagramsUnavailable
//   - ControlMessage
//   - AudioControlProtocol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/quic-go/quic-go"
)

// WaitForDatagramConnTimeout bounds how long NewLibp2pTransport will wait
// for a direct QUIC connection to appear before giving up. When two peers
// can only reach each other via libp2p's circuit relay (cross-network,
// behind NAT), the initial Connect succeeds via /p2p-circuit but no QUIC
// datagrams traverse the relay. libp2p's DCUtR (hole-punching) then runs
// in the background and, when both NATs cooperate, upgrades the relay
// path to a direct QUIC connection. This timeout gives DCUtR enough time
// to complete one full attempt; 8s is conservative for typical home NATs
// and still bounds the worst case (symmetric-NAT-on-both-sides → unfix‑
// able) from blocking call setup indefinitely.
const WaitForDatagramConnTimeout = 8 * time.Second

// datagramPollInterval is how often waitForDatagramConn re-checks the
// connection set while waiting. Short enough that DCUtR success is picked
// up promptly; not so short it busy-loops.
const datagramPollInterval = 100 * time.Millisecond

// AudioControlProtocol is the libp2p stream protocol for audio control messages.
const AudioControlProtocol = protocol.ID("/opencom/audio-control/1.0.0")

// ErrDatagramsUnavailable is returned by NewLibp2pTransport when none of the
// connections to the remote peer expose a QUIC datagram interface. This happens
// when the connection uses TCP or the WebTransport transport instead of QUIC v1.
var ErrDatagramsUnavailable = errors.New("connection has no datagram support")

// ControlMessage is a JSON-encoded control message sent over the reliable
// audio-control stream. Both mute-state changes and stats snapshots share
// this envelope.
type ControlMessage struct {
	Type  string `json:"type"`
	Value bool   `json:"value,omitempty"`
	Stats *Stats `json:"stats,omitempty"`
}

// Transport is the per-call audio transport. It provides:
//   - unreliable, low-latency QUIC datagrams for audio media frames
//   - a reliable libp2p stream for control messages (mute, stats)
type Transport interface {
	// SendDatagram sends b as a QUIC datagram. Unreliable, low-latency.
	SendDatagram(b []byte) error
	// RecvDatagram blocks until a datagram arrives or ctx expires.
	RecvDatagram(ctx context.Context) ([]byte, error)
	// SendControl encodes msg as JSON and writes it to the control stream.
	SendControl(msg ControlMessage) error
	// Control returns a read-only channel that delivers inbound control messages.
	Control() <-chan ControlMessage
	// Close shuts down both the datagram path and the control stream.
	Close() error
}

// datagramConn is the subset of *quic.Conn we use for audio media.
type datagramConn interface {
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
}

// streamRegistry maps (host → (remotePeer → channel)) for inbound control
// streams. RegisterStreamHandler installs a SetStreamHandler that deposits
// accepted network.Streams into this registry; readControlLoop drains from
// it to get the peer's stream as our read side.
var (
	regMu    sync.Mutex
	registry = map[host.Host]map[peer.ID]chan network.Stream{}
)

// RegisterStreamHandler installs the /opencom/audio-control/1.0.0 stream
// handler on h. It must be called once per host (daemon startup, test setup)
// before any NewLibp2pTransport call that expects to receive control messages.
//
// The handler buffers at most 8 pending inbound streams per remote peer.
func RegisterStreamHandler(h host.Host) {
	regMu.Lock()
	if _, ok := registry[h]; !ok {
		registry[h] = make(map[peer.ID]chan network.Stream)
	}
	regMu.Unlock()

	h.SetStreamHandler(AudioControlProtocol, func(s network.Stream) {
		remote := s.Conn().RemotePeer()
		ch := inboundChan(h, remote)
		ch <- s // blocks only if channel full (capacity 8); practically instant
	})
}

// inboundChan returns (creating if necessary) the buffered channel for
// inbound control streams from remote to host h.
func inboundChan(h host.Host, remote peer.ID) chan network.Stream {
	regMu.Lock()
	defer regMu.Unlock()
	peers, ok := registry[h]
	if !ok {
		peers = make(map[peer.ID]chan network.Stream)
		registry[h] = peers
	}
	ch, ok := peers[remote]
	if !ok {
		ch = make(chan network.Stream, 8)
		peers[remote] = ch
	}
	return ch
}

// libp2pTransport implements Transport.
type libp2pTransport struct {
	dconn   datagramConn  // underlying QUIC connection for datagrams
	outCtrl network.Stream // our outgoing control stream (write side)
	outEnc  *json.Encoder  // JSON encoder on outCtrl
	outMu   sync.Mutex     // serialises SendControl writes

	controlCh chan ControlMessage // inbound control messages, closed on Close
	closeOnce sync.Once
	closeCtx  context.Context
	closeFn   context.CancelFunc
}

// NewLibp2pTransport creates a Transport to peer p using host h.
//
// Prerequisites:
//  1. h must have at least one QUIC v1 connection to p (for datagrams),
//     or be able to obtain one via DCUtR within WaitForDatagramConnTimeout.
//  2. RegisterStreamHandler must have been called on h so inbound control
//     streams from p are delivered to the per-peer channel.
//
// NewLibp2pTransport opens its own outgoing control stream to p (write side)
// and starts a goroutine that waits for p's reciprocal stream (read side) to
// appear in h's inbound channel. Both sides open their stream concurrently so
// there is no deadlock when the caller calls NewLibp2pTransport for A then B.
//
// Returns ErrDatagramsUnavailable if no QUIC connection appears within
// WaitForDatagramConnTimeout (typical when peers are behind symmetric NATs
// that DCUtR can't hole-punch through).
func NewLibp2pTransport(ctx context.Context, h host.Host, p peer.ID) (Transport, error) {
	// 1. Find a QUIC datagram-capable connection. This blocks up to
	//    WaitForDatagramConnTimeout to give DCUtR a chance to upgrade
	//    a relay-only connection to direct QUIC.
	dconn, err := waitForDatagramConn(ctx, h, p, WaitForDatagramConnTimeout)
	if err != nil {
		return nil, err
	}

	// 2. Open our outgoing control stream to p (write side).
	// Allow opening over a libp2p "limited" (relay-v2) connection.
	// Without this opt-in, libp2p's swarm refuses to open streams on
	// relayed connections and waits for DCUtR to upgrade them, which
	// often deadlocks the audio-session setup behind the call control
	// plane. Datagrams still go over the direct QUIC connection that
	// findDatagramConn picked above, regardless of this flag.
	streamCtx := network.WithAllowLimitedConn(ctx, "opencom-audio-control")
	outStream, err := h.NewStream(streamCtx, p, AudioControlProtocol)
	if err != nil {
		return nil, fmt.Errorf("opening audio-control stream to %s: %w", p, err)
	}

	closeCtx, closeFn := context.WithCancel(context.Background())
	t := &libp2pTransport{
		dconn:     dconn,
		outCtrl:   outStream,
		outEnc:    json.NewEncoder(outStream),
		controlCh: make(chan ControlMessage, 32),
		closeCtx:  closeCtx,
		closeFn:   closeFn,
	}

	// 3. Start the read side in a goroutine: wait for p's inbound stream,
	//    then pump messages into controlCh. This avoids blocking the caller
	//    (and therefore avoids a deadlock when both sides create transports
	//    sequentially in a test).
	inCh := inboundChan(h, p)
	go t.waitAndReadControlLoop(inCh)

	return t, nil
}

// waitForDatagramConn polls findDatagramConn until either a direct QUIC
// connection appears, ctx is cancelled, or timeout elapses. The poll
// interval (datagramPollInterval) is short enough that DCUtR success is
// noticed promptly; libp2p does not currently expose a "datagram-capable
// connection added" event, so polling is the most portable signal.
//
// Returns the underlying datagram connection on success, ctx.Err() if
// the caller's context is cancelled, or ErrDatagramsUnavailable on
// timeout.
func waitForDatagramConn(ctx context.Context, h host.Host, p peer.ID, timeout time.Duration) (datagramConn, error) {
	if dconn, err := findDatagramConn(h, p); err == nil {
		return dconn, nil
	}
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(datagramPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
			if dconn, err := findDatagramConn(h, p); err == nil {
				return dconn, nil
			}
			if time.Now().After(deadline) {
				return nil, ErrDatagramsUnavailable
			}
		}
	}
}

// findDatagramConn iterates the live connections to p and returns the first
// one that exposes the QUIC datagram interface via network.Conn.As.
func findDatagramConn(h host.Host, p peer.ID) (datagramConn, error) {
	for _, c := range h.Network().ConnsToPeer(p) {
		var qc *quic.Conn
		if c.As(&qc) {
			return qc, nil
		}
	}
	return nil, ErrDatagramsUnavailable
}

// SendDatagram implements Transport.
func (t *libp2pTransport) SendDatagram(b []byte) error {
	return t.dconn.SendDatagram(b)
}

// RecvDatagram implements Transport.
func (t *libp2pTransport) RecvDatagram(ctx context.Context) ([]byte, error) {
	return t.dconn.ReceiveDatagram(ctx)
}

// SendControl implements Transport.
func (t *libp2pTransport) SendControl(msg ControlMessage) error {
	t.outMu.Lock()
	defer t.outMu.Unlock()
	return t.outEnc.Encode(msg)
}

// Control implements Transport.
func (t *libp2pTransport) Control() <-chan ControlMessage {
	return t.controlCh
}

// Close implements Transport.
func (t *libp2pTransport) Close() error {
	var firstErr error
	t.closeOnce.Do(func() {
		t.closeFn() // signals waitAndReadControlLoop to stop
		if err := t.outCtrl.Close(); err != nil {
			firstErr = err
		}
	})
	return firstErr
}

// waitAndReadControlLoop waits for the peer's inbound control stream to arrive
// in inCh, then reads JSON ControlMessages from it until the stream closes or
// the transport is closed.
func (t *libp2pTransport) waitAndReadControlLoop(inCh <-chan network.Stream) {
	// Wait for the peer's outgoing stream (our read side).
	var inStream network.Stream
	select {
	case inStream = <-inCh:
	case <-t.closeCtx.Done():
		close(t.controlCh)
		return
	}

	defer func() {
		_ = inStream.Close()
		// Close controlCh only once; avoid double-close if Close() was called.
		select {
		case <-t.closeCtx.Done():
			// Transport was closed; channel may already be draining.
		default:
		}
		// Safe to close here: only this goroutine writes to/closes controlCh.
		close(t.controlCh)
	}()

	scanner := bufio.NewScanner(inStream)
	scanner.Buffer(make([]byte, 0, 4*1024), 1<<20)
	for scanner.Scan() {
		var msg ControlMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		select {
		case t.controlCh <- msg:
		case <-t.closeCtx.Done():
			return
		}
	}
}
