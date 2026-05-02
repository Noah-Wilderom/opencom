package audio_test

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"

	"opencom/internal/audio"
	"opencom/internal/identity"
	"opencom/internal/transport/p2p"
)

// noRelays is an empty (non-nil) slice that disables AutoRelay so
// tests stay off the public network and start quickly.
var noRelays = []peer.AddrInfo{}

// quicOnlyListen forces a host to bind QUIC v1 only on loopback so
// libp2p's swarm has a direct datagram-capable connection to use.
func quicOnlyListen(t *testing.T) []ma.Multiaddr {
	t.Helper()
	m, err := ma.NewMultiaddr("/ip4/127.0.0.1/udp/0/quic-v1")
	assert.NoError(t, err)
	return []ma.Multiaddr{m}
}

// tcpOnlyListen forces a host to bind TCP only on loopback so libp2p
// can NOT find a datagram-capable connection — exactly the
// reliable-stream-only fallback path we want to exercise.
func tcpOnlyListen(t *testing.T) []ma.Multiaddr {
	t.Helper()
	m, err := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/0")
	assert.NoError(t, err)
	return []ma.Multiaddr{m}
}

func makeHost(t *testing.T, ctx context.Context, listen []ma.Multiaddr) *p2p.Host {
	t.Helper()
	kp, err := identity.Generate()
	assert.NoError(t, err)
	h, err := p2p.New(ctx, p2p.HostOptions{
		PrivKey:     kp.Priv,
		ListenAddrs: listen,
		RelayPeers:  noRelays,
	})
	assert.NoError(t, err)
	if h == nil {
		t.FailNow()
	}
	t.Cleanup(func() { h.Close() })
	return h
}

// TestTransport_MediaOverDatagrams: with a direct QUIC connection
// available, the transport should attach the datagram fast-path and
// MediaMode should report "datagram".
func TestTransport_MediaOverDatagrams(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hA := makeHost(t, ctx, quicOnlyListen(t))
	hB := makeHost(t, ctx, quicOnlyListen(t))

	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)
	assert.NoError(t, hA.Connect(ctx, bInfo))

	audio.RegisterStreamHandler(hA.HostInternal())
	audio.RegisterStreamHandler(hB.HostInternal())

	tA, err := audio.NewLibp2pTransport(ctx, hA.HostInternal(), hB.ID())
	assert.NoError(t, err)
	defer tA.Close()
	tB, err := audio.NewLibp2pTransport(ctx, hB.HostInternal(), hA.ID())
	assert.NoError(t, err)
	defer tB.Close()

	// Wait for the datagram fast-path to attach (datagramWatcher
	// runs in the background; first poll may not have fired yet).
	assert.Eventually(t, func() bool {
		return tA.MediaMode() == "datagram" && tB.MediaMode() == "datagram"
	}, 2*time.Second, 50*time.Millisecond, "both transports should attach the datagram path")

	payload := []byte("audio frame test")
	assert.NoError(t, tA.SendMedia(payload))

	rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
	defer rcancel()
	got, err := tB.RecvMedia(rctx)
	assert.NoError(t, err)
	assert.Equal(t, payload, got)
}

// TestTransport_MediaOverReliableStream: with TCP-only hosts (no QUIC
// → no datagrams), the transport should fall back to the reliable
// media stream and MediaMode should report "stream".
func TestTransport_MediaOverReliableStream(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	hA := makeHost(t, ctx, tcpOnlyListen(t))
	hB := makeHost(t, ctx, tcpOnlyListen(t))

	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)
	assert.NoError(t, hA.Connect(ctx, bInfo))

	audio.RegisterStreamHandler(hA.HostInternal())
	audio.RegisterStreamHandler(hB.HostInternal())

	tA, err := audio.NewLibp2pTransport(ctx, hA.HostInternal(), hB.ID())
	assert.NoError(t, err)
	defer tA.Close()
	tB, err := audio.NewLibp2pTransport(ctx, hB.HostInternal(), hA.ID())
	assert.NoError(t, err)
	defer tB.Close()

	// Should immediately be in stream mode (datagramWatcher's tight
	// poll won't ever find a QUIC conn here; it's fine, the stream
	// path doesn't depend on the watcher).
	assert.Equal(t, "stream", tA.MediaMode())
	assert.Equal(t, "stream", tB.MediaMode())

	payload := []byte("hello via reliable stream")
	assert.NoError(t, tA.SendMedia(payload))

	rctx, rcancel := context.WithTimeout(ctx, 3*time.Second)
	defer rcancel()
	got, err := tB.RecvMedia(rctx)
	assert.NoError(t, err)
	assert.Equal(t, payload, got)
}

// TestTransport_MediaUpgradesToDatagramsMidCall verifies the
// upgrade path behaviour at the seam where it actually matters: when
// findDatagramConn starts returning a datagram-capable connection,
// the watcher should attach it and MediaMode should flip from "stream"
// to "datagram". Real libp2p is uncooperative about producing a
// second transport-type connection to the same peer.ID in a unit
// test (Connect dedupes by peer, swarm dedupes by addr), so we drive
// the seam directly via an exported test hook that flips a fake
// datagram conn into place. The DCUtR happy path itself is covered
// by manual cross-network testing.
func TestTransport_MediaUpgradesToDatagramsMidCall(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hA := makeHost(t, ctx, tcpOnlyListen(t))
	hB := makeHost(t, ctx, tcpOnlyListen(t))

	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)
	assert.NoError(t, hA.Connect(ctx, bInfo))

	audio.RegisterStreamHandler(hA.HostInternal())
	audio.RegisterStreamHandler(hB.HostInternal())

	tA, err := audio.NewLibp2pTransport(ctx, hA.HostInternal(), hB.ID())
	assert.NoError(t, err)
	defer tA.Close()

	// Initially: stream-only, no datagram path.
	assert.Equal(t, "stream", tA.MediaMode())

	// Simulate "DCUtR succeeded mid-call" by directly attaching a
	// fake datagram conn via the test hook. The recv pump for it
	// blocks on ReceiveDatagram which will never fire from our fake;
	// we only care that MediaMode reports the upgrade.
	audio.AttachFakeDatagramForTest(tA, &nopDatagramConn{})

	assert.Equal(t, "datagram", tA.MediaMode(),
		"MediaMode should flip immediately once a datagram conn is attached")
}

// nopDatagramConn is a datagramConn-shaped no-op for the upgrade test.
// SendDatagram silently succeeds; ReceiveDatagram blocks until ctx
// cancels (the recv pump exits then).
type nopDatagramConn struct{}

func (nopDatagramConn) SendDatagram([]byte) error { return nil }
func (nopDatagramConn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// Unused but required to keep the network import live (used by the
// peer/network/ma helpers in other tests).
var _ = network.WithForceDirectDial

// TestTransport_MediaDropsOnBackpressure: when sendCh is saturated,
// SendMedia returns ErrMediaBackpressure rather than blocking. The
// transport never blocks the audio capture thread.
func TestTransport_MediaDropsOnBackpressure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hA := makeHost(t, ctx, tcpOnlyListen(t))
	hB := makeHost(t, ctx, tcpOnlyListen(t))

	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)
	assert.NoError(t, hA.Connect(ctx, bInfo))

	audio.RegisterStreamHandler(hA.HostInternal())
	audio.RegisterStreamHandler(hB.HostInternal())

	tA, err := audio.NewLibp2pTransport(ctx, hA.HostInternal(), hB.ID())
	assert.NoError(t, err)
	defer tA.Close()
	// Note: deliberately do NOT construct tB. Without a peer, no
	// recv pump on B drains; A's reliable stream writes will
	// eventually back up the sendPump, the sendCh fills, and
	// subsequent SendMedia calls return ErrMediaBackpressure.

	// Spam frames. Some will succeed, some will be dropped. We just
	// need to prove that AT LEAST ONE returns ErrMediaBackpressure
	// rather than blocking.
	deadline := time.Now().Add(2 * time.Second)
	gotBackpressure := false
	for time.Now().Before(deadline) {
		err := tA.SendMedia([]byte("filler frame for backpressure test xxxxxx"))
		if err == audio.ErrMediaBackpressure {
			gotBackpressure = true
			break
		}
	}
	assert.True(t, gotBackpressure, "SendMedia should hit ErrMediaBackpressure when no peer is draining")
}

func TestTransport_ControlMessageRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hA := makeHost(t, ctx, quicOnlyListen(t))
	hB := makeHost(t, ctx, quicOnlyListen(t))

	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)
	assert.NoError(t, hA.Connect(ctx, bInfo))

	audio.RegisterStreamHandler(hA.HostInternal())
	audio.RegisterStreamHandler(hB.HostInternal())

	tA, err := audio.NewLibp2pTransport(ctx, hA.HostInternal(), hB.ID())
	assert.NoError(t, err)
	defer tA.Close()
	tB, err := audio.NewLibp2pTransport(ctx, hB.HostInternal(), hA.ID())
	assert.NoError(t, err)
	defer tB.Close()

	assert.NoError(t, tA.SendControl(audio.ControlMessage{Type: "mute", Value: true}))

	select {
	case msg, ok := <-tB.Control():
		assert.True(t, ok)
		assert.Equal(t, "mute", msg.Type)
		assert.True(t, msg.Value)
	case <-time.After(2 * time.Second):
		t.Fatal("no control message received")
	}
}
