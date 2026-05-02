package audio

// transport.go — dual-stream + dual-path audio transport.
//
// Two libp2p stream protocols, both opened per-call:
//   - /opencom/audio-control/1.0.0  reliable: mute, stats, peer-mute
//   - /opencom/audio-media/1.0.0    reliable: RTP/Opus media frames
//
// Plus an optional fast-path overlay: when a direct QUIC connection
// exists (or appears mid-call via DCUtR), media frames bypass the
// reliable stream and go over QUIC datagrams instead.
//
// Send semantics:
//   sendPump pulls from sendCh and prefers the datagram path when
//   t.dconn is non-nil. Otherwise it falls back to the reliable stream.
//   The choice is per-frame, so DCUtR success mid-call upgrades the
//   next frame transparently.
//
// Recv semantics:
//   recvCh is a shared bounded queue. Up to two pumps push into it:
//     - recvPumpDatagram (started when t.dconn becomes non-nil)
//     - recvPumpStream   (started when the peer's inbound media stream
//                         shows up via the per-(host,proto,peer) registry)
//   Duplicate frames (same RTP sequence on both paths) are deduped by
//   the jitter buffer's seqDelta-aware Push.
//
// Backpressure:
//   sendCh and recvCh are bounded. SendMedia returns
//   ErrMediaBackpressure when sendCh is full (audio-real-time: drop is
//   better than block). recvCh evicts oldest when full.
//
// Concurrency note: NewLibp2pTransport does NOT block waiting for the
// peer's reciprocal streams. Goroutines wait on per-protocol inbound
// channels populated by RegisterStreamHandler, so both peers can call
// NewLibp2pTransport in either order without deadlocking.

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/quic-go/quic-go"
)

// WaitForDatagramConnTimeout is the initial tight-poll window during
// which the transport tries to attach a datagram fast-path before
// settling into a slower background watch. 8s catches DCUtR's typical
// hole-punch latency without delaying call setup beyond that bound.
const WaitForDatagramConnTimeout = 8 * time.Second

// datagramPollInterval is how often the initial tight poll re-checks
// findDatagramConn. Short enough to notice DCUtR success promptly,
// long enough to not busy-loop.
const datagramPollInterval = 100 * time.Millisecond

// dcutrWatchInterval is how often the long-running watcher (active for
// the call's lifetime after the initial tight window) re-checks for a
// newly-available direct QUIC connection. Lower frequency keeps the
// background work negligible.
const dcutrWatchInterval = 2 * time.Second

// Protocol IDs.
const (
	AudioControlProtocol = protocol.ID("/opencom/audio-control/1.0.0")
	AudioMediaProtocol   = protocol.ID("/opencom/audio-media/1.0.0")
)

// Channel capacities. Each frame is one 20ms Opus packet, so 16 frames
// = ~320ms of buffered audio — enough to absorb a short stall, small
// enough to avoid noticeable lag when the receiver catches up.
const (
	mediaSendCap = 16
	mediaRecvCap = 16
)

// maxMediaFrameSize bounds a single framed media payload. RTP/Opus
// 20ms voice fits comfortably in a few hundred bytes; 1500 leaves
// headroom for richer codecs without permitting absurd allocations.
const maxMediaFrameSize = 1500

// ErrDatagramsUnavailable is returned by waitForDatagramConn when no
// QUIC connection appears within the deadline. The transport itself
// no longer surfaces this error — it falls back to the reliable media
// stream — but the constant is retained for callers that want the
// underlying signal (tests, diagnostics).
var ErrDatagramsUnavailable = errors.New("connection has no datagram support")

// ErrMediaBackpressure is returned by SendMedia when the send buffer
// is full. Real-time audio: drop is better than block.
var ErrMediaBackpressure = errors.New("media send buffer full")

// ErrTransportClosed is returned by RecvMedia when Close has been called.
var ErrTransportClosed = errors.New("transport closed")

// ControlMessage is a JSON-encoded control message sent over the
// reliable audio-control stream. Both mute-state changes and stats
// snapshots share this envelope.
type ControlMessage struct {
	Type  string `json:"type"`
	Value bool   `json:"value,omitempty"`
	Stats *Stats `json:"stats,omitempty"`
}

// Transport is the per-call audio transport.
//
// Media (SendMedia/RecvMedia) automatically picks the best available
// path: QUIC datagrams when a direct connection exists, libp2p
// reliable stream otherwise. Callers don't have to care which.
//
// Control (SendControl/Control) is always reliable and ordered.
type Transport interface {
	// SendMedia enqueues a frame for transmission. Non-blocking;
	// returns ErrMediaBackpressure if the send buffer is full
	// (frame dropped).
	SendMedia(b []byte) error
	// RecvMedia blocks until a frame arrives, ctx expires, or the
	// transport closes.
	RecvMedia(ctx context.Context) ([]byte, error)
	// MediaMode reports the current send-side path: "datagram" when
	// frames are going over QUIC, "stream" when they're going over
	// the reliable libp2p stream, "none" when neither is ready.
	MediaMode() string

	// SendControl encodes msg as JSON and writes it to the control stream.
	SendControl(msg ControlMessage) error
	// Control returns a read-only channel that delivers inbound control messages.
	Control() <-chan ControlMessage

	// Close shuts down all paths and goroutines. Idempotent.
	Close() error
}

// datagramConn is the subset of *quic.Conn we use for media frames.
type datagramConn interface {
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
}

// streamRegistry: (host → protocol → peer → channel of inbound streams).
// RegisterStreamHandler installs a SetStreamHandler per protocol that
// deposits accepted streams here; the matching wait loop in
// libp2pTransport drains the channel.
var (
	regMu    sync.Mutex
	registry = map[host.Host]map[protocol.ID]map[peer.ID]chan network.Stream{}
)

// RegisterStreamHandler installs handlers for both audio-control and
// audio-media protocols on h. Call once per host at daemon startup
// (and in test setup) before any NewLibp2pTransport call expects to
// receive inbound traffic.
//
// The handlers buffer at most 8 pending inbound streams per (proto,
// remote peer) pair.
func RegisterStreamHandler(h host.Host) {
	registerProto(h, AudioControlProtocol)
	registerProto(h, AudioMediaProtocol)
}

func registerProto(h host.Host, p protocol.ID) {
	h.SetStreamHandler(p, func(s network.Stream) {
		remote := s.Conn().RemotePeer()
		ch := inboundChan(h, p, remote)
		ch <- s // blocks only if channel full (cap 8); practically instant
	})
}

// inboundChan returns (creating if needed) the buffered channel for
// inbound streams of protocol p from remote, on host h.
func inboundChan(h host.Host, p protocol.ID, remote peer.ID) chan network.Stream {
	regMu.Lock()
	defer regMu.Unlock()
	byProto, ok := registry[h]
	if !ok {
		byProto = make(map[protocol.ID]map[peer.ID]chan network.Stream)
		registry[h] = byProto
	}
	byPeer, ok := byProto[p]
	if !ok {
		byPeer = make(map[peer.ID]chan network.Stream)
		byProto[p] = byPeer
	}
	ch, ok := byPeer[remote]
	if !ok {
		ch = make(chan network.Stream, 8)
		byPeer[remote] = ch
	}
	return ch
}

// libp2pTransport implements Transport.
type libp2pTransport struct {
	h host.Host
	p peer.ID

	// Control plane: dual reliable streams (each side opens its own
	// outgoing). controlCh is closed exactly once when the inbound
	// reader exits or Close fires.
	outCtrl    network.Stream
	outCtrlEnc *json.Encoder
	outCtrlMu  sync.Mutex
	controlCh  chan ControlMessage
	controlChClosed sync.Once

	// Media plane: shared bounded queues drained by Pipeline.
	sendCh chan []byte
	recvCh chan []byte

	// Datagram fast-path overlay. Populated lazily by datagramWatcher
	// once findDatagramConn returns success.
	dconnMu sync.RWMutex
	dconn   datagramConn

	// Reliable media stream (always opened). The outbound is our
	// write side; an inbound stream from the peer drives the recv
	// pump on the other side.
	outMedia    network.Stream
	outMediaMu  sync.Mutex

	// Lifecycle.
	closeOnce sync.Once
	closeCtx  context.Context
	closeFn   context.CancelFunc
	wg        sync.WaitGroup
}

// NewLibp2pTransport creates a Transport to peer p using host h.
//
// Prerequisites:
//  1. h has at least one usable libp2p connection to p (direct or
//     relayed). Direct QUIC enables the datagram fast-path; relay-only
//     still works via the reliable media stream.
//  2. RegisterStreamHandler has been called on h so inbound control
//     and media streams are routed into the per-protocol channels.
//
// Returns immediately after opening the outgoing control + media
// streams. The datagram fast-path is attached asynchronously by
// datagramWatcher (tight poll for WaitForDatagramConnTimeout, then
// slow watch for the lifetime of the transport so DCUtR success
// late-in-the-call still upgrades us).
func NewLibp2pTransport(ctx context.Context, h host.Host, p peer.ID) (Transport, error) {
	closeCtx, closeFn := context.WithCancel(context.Background())
	t := &libp2pTransport{
		h:         h,
		p:         p,
		controlCh: make(chan ControlMessage, 32),
		sendCh:    make(chan []byte, mediaSendCap),
		recvCh:    make(chan []byte, mediaRecvCap),
		closeCtx:  closeCtx,
		closeFn:   closeFn,
	}

	// 1. Open outgoing control stream. Allow over a libp2p limited
	//    (relay-v2) connection — the JSON control plane is small and
	//    survives circuit-relay just fine.
	ctrlCtx := network.WithAllowLimitedConn(ctx, "opencom-audio-control")
	outCtrl, err := h.NewStream(ctrlCtx, p, AudioControlProtocol)
	if err != nil {
		closeFn()
		return nil, fmt.Errorf("opening audio-control stream to %s: %w", p, err)
	}
	t.outCtrl = outCtrl
	t.outCtrlEnc = json.NewEncoder(outCtrl)

	// 2. Open outgoing media stream (always). Datagram fast-path
	//    attaches alongside; this stream stays as the always-on
	//    fallback so a mid-call DCUtR regression doesn't drop audio.
	mediaCtx := network.WithAllowLimitedConn(ctx, "opencom-audio-media")
	outMedia, err := h.NewStream(mediaCtx, p, AudioMediaProtocol)
	if err != nil {
		_ = outCtrl.Close()
		closeFn()
		return nil, fmt.Errorf("opening audio-media stream to %s: %w", p, err)
	}
	t.outMedia = outMedia

	// 3. Pumps + waiters.
	t.wg.Add(4)
	go t.sendPump()
	go t.waitAndReadControlLoop(inboundChan(h, AudioControlProtocol, p))
	go t.waitAndReadMediaLoop(inboundChan(h, AudioMediaProtocol, p))
	go t.datagramWatcher()

	return t, nil
}

// SendMedia implements Transport.
func (t *libp2pTransport) SendMedia(b []byte) error {
	cp := append([]byte(nil), b...)
	select {
	case t.sendCh <- cp:
		return nil
	default:
		return ErrMediaBackpressure
	}
}

// RecvMedia implements Transport.
func (t *libp2pTransport) RecvMedia(ctx context.Context) ([]byte, error) {
	select {
	case b := <-t.recvCh:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.closeCtx.Done():
		return nil, ErrTransportClosed
	}
}

// MediaMode implements Transport.
func (t *libp2pTransport) MediaMode() string {
	t.dconnMu.RLock()
	dc := t.dconn
	t.dconnMu.RUnlock()
	if dc != nil {
		return "datagram"
	}
	t.outMediaMu.Lock()
	s := t.outMedia
	t.outMediaMu.Unlock()
	if s != nil {
		return "stream"
	}
	return "none"
}

// SendControl implements Transport.
func (t *libp2pTransport) SendControl(msg ControlMessage) error {
	t.outCtrlMu.Lock()
	defer t.outCtrlMu.Unlock()
	return t.outCtrlEnc.Encode(msg)
}

// Control implements Transport.
func (t *libp2pTransport) Control() <-chan ControlMessage {
	return t.controlCh
}

// Close implements Transport. Idempotent.
func (t *libp2pTransport) Close() error {
	var firstErr error
	t.closeOnce.Do(func() {
		t.closeFn() // unblocks pumps that select on closeCtx
		// Close outbound streams to wake up pumps and signal peer.
		if t.outCtrl != nil {
			if err := t.outCtrl.Close(); err != nil {
				firstErr = err
			}
		}
		if t.outMedia != nil {
			if err := t.outMedia.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	})
	return firstErr
}

// sendPump pulls frames from sendCh and routes them to the best
// available path. Per-frame check of t.dconn means a mid-call DCUtR
// success upgrades the next frame with no plumbing required.
func (t *libp2pTransport) sendPump() {
	defer t.wg.Done()
	for {
		select {
		case <-t.closeCtx.Done():
			return
		case b := <-t.sendCh:
			t.dconnMu.RLock()
			dc := t.dconn
			t.dconnMu.RUnlock()
			if dc != nil {
				_ = dc.SendDatagram(b)
				continue
			}
			t.outMediaMu.Lock()
			s := t.outMedia
			t.outMediaMu.Unlock()
			if s != nil {
				_ = writeFramedMedia(s, b)
			}
		}
	}
}

// recvPumpDatagram drains datagrams into recvCh. Started once when a
// direct QUIC connection becomes available.
func (t *libp2pTransport) recvPumpDatagram(dc datagramConn) {
	defer t.wg.Done()
	for {
		b, err := dc.ReceiveDatagram(t.closeCtx)
		if err != nil {
			return
		}
		t.deliverRecv(b)
	}
}

// recvPumpStream reads length-framed media from the peer's inbound
// media stream and forwards into recvCh.
func (t *libp2pTransport) recvPumpStream(s network.Stream) {
	defer t.wg.Done()
	defer func() { _ = s.Close() }()
	r := bufio.NewReader(s)
	for {
		b, err := readFramedMedia(r)
		if err != nil {
			return
		}
		t.deliverRecv(b)
	}
}

// deliverRecv pushes b into recvCh, evicting the oldest queued frame
// if the channel is full. Real-time audio: a fresh frame is more
// useful than a stale one.
func (t *libp2pTransport) deliverRecv(b []byte) {
	select {
	case t.recvCh <- b:
		return
	default:
	}
	// Channel full — pop oldest, then push new (best effort).
	select {
	case <-t.recvCh:
	default:
	}
	select {
	case t.recvCh <- b:
	default:
	}
}

// datagramWatcher attaches the datagram fast-path as soon as a direct
// QUIC connection exists. Two phases:
//
//	1. Tight poll for WaitForDatagramConnTimeout — catches the common
//	   case of DCUtR succeeding during call setup.
//	2. Slow poll (dcutrWatchInterval) for the lifetime of the
//	   transport — catches DCUtR succeeding mid-call (NAT mappings
//	   shift, hole-punching retries succeed).
//
// Once a datagram path attaches, the watcher exits.
func (t *libp2pTransport) datagramWatcher() {
	defer t.wg.Done()
	if t.tryAttachDatagram() {
		return
	}
	tightDeadline := time.Now().Add(WaitForDatagramConnTimeout)
	for time.Now().Before(tightDeadline) {
		select {
		case <-t.closeCtx.Done():
			return
		case <-time.After(datagramPollInterval):
			if t.tryAttachDatagram() {
				return
			}
		}
	}
	tick := time.NewTicker(dcutrWatchInterval)
	defer tick.Stop()
	for {
		select {
		case <-t.closeCtx.Done():
			return
		case <-tick.C:
			if t.tryAttachDatagram() {
				return
			}
		}
	}
}

// tryAttachDatagram looks for a direct QUIC connection and, if one
// exists and we haven't already attached, atomically attaches it and
// starts the matching recv pump. Returns true if a path is now (or
// already was) attached.
func (t *libp2pTransport) tryAttachDatagram() bool {
	dc, err := findDatagramConn(t.h, t.p)
	if err != nil {
		return false
	}
	t.dconnMu.Lock()
	if t.dconn != nil {
		t.dconnMu.Unlock()
		return true
	}
	t.dconn = dc
	t.dconnMu.Unlock()
	t.wg.Add(1)
	go t.recvPumpDatagram(dc)
	return true
}

// findDatagramConn returns the first live QUIC connection to p that
// exposes a datagram interface, or ErrDatagramsUnavailable if none.
func findDatagramConn(h host.Host, p peer.ID) (datagramConn, error) {
	for _, c := range h.Network().ConnsToPeer(p) {
		var qc *quic.Conn
		if c.As(&qc) {
			return qc, nil
		}
	}
	return nil, ErrDatagramsUnavailable
}

// waitAndReadControlLoop blocks until the peer's outgoing control
// stream is delivered to inCh, then reads JSON control messages from
// it until the stream errors or the transport closes.
func (t *libp2pTransport) waitAndReadControlLoop(inCh <-chan network.Stream) {
	defer t.wg.Done()
	var inStream network.Stream
	select {
	case inStream = <-inCh:
	case <-t.closeCtx.Done():
		t.controlChClosed.Do(func() { close(t.controlCh) })
		return
	}

	// Force-close the inbound stream when the transport closes so the
	// scanner unblocks (Scan doesn't honour ctx).
	go func() {
		<-t.closeCtx.Done()
		_ = inStream.Close()
	}()

	defer t.controlChClosed.Do(func() { close(t.controlCh) })
	defer func() { _ = inStream.Close() }()

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

// waitAndReadMediaLoop blocks until the peer's outgoing media stream
// is delivered to inCh, then hands it to recvPumpStream.
func (t *libp2pTransport) waitAndReadMediaLoop(inCh <-chan network.Stream) {
	defer t.wg.Done()
	var inStream network.Stream
	select {
	case inStream = <-inCh:
	case <-t.closeCtx.Done():
		return
	}

	// Force-close on transport shutdown so ReadFull unblocks.
	go func() {
		<-t.closeCtx.Done()
		_ = inStream.Close()
	}()

	t.wg.Add(1)
	t.recvPumpStream(inStream)
}

// writeFramedMedia writes a length-prefixed media frame.
//   layout: uint16 big-endian length || payload
func writeFramedMedia(w io.Writer, b []byte) error {
	if len(b) > maxMediaFrameSize {
		return fmt.Errorf("media frame too large: %d > %d", len(b), maxMediaFrameSize)
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

// readFramedMedia reads one length-prefixed frame from r.
func readFramedMedia(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(hdr[:])
	if n == 0 {
		return nil, nil
	}
	if n > maxMediaFrameSize {
		return nil, fmt.Errorf("media frame too large: %d > %d", n, maxMediaFrameSize)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
