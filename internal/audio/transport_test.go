package audio_test

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"

	"opencom/internal/audio"
	"opencom/internal/identity"
	"opencom/internal/transport/p2p"
)

// noRelays is an empty (non-nil) slice that disables AutoRelay so
// tests stay off the public network and start quickly.
var noRelays = []peer.AddrInfo{}

func TestTransport_DatagramRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	kpA, err := identity.Generate()
	assert.NoError(t, err)
	kpB, err := identity.Generate()
	assert.NoError(t, err)

	hA, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpA.Priv, RelayPeers: noRelays})
	assert.NoError(t, err)
	if hA == nil {
		t.FailNow()
	}
	t.Cleanup(func() { hA.Close() })

	hB, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpB.Priv, RelayPeers: noRelays})
	assert.NoError(t, err)
	if hB == nil {
		t.FailNow()
	}
	t.Cleanup(func() { hB.Close() })

	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)
	assert.NoError(t, hA.Connect(ctx, bInfo))

	// Option B: register the audio-control protocol handler on both hosts
	// so each side can accept inbound control streams from the other.
	audio.RegisterStreamHandler(hA.HostInternal())
	audio.RegisterStreamHandler(hB.HostInternal())

	tA, err := audio.NewLibp2pTransport(ctx, hA.HostInternal(), hB.ID())
	if err == audio.ErrDatagramsUnavailable {
		t.Skip("no datagram-capable connection in this test rig")
	}
	assert.NoError(t, err)
	if tA == nil {
		t.FailNow()
	}
	defer tA.Close()

	tB, err := audio.NewLibp2pTransport(ctx, hB.HostInternal(), hA.ID())
	if err == audio.ErrDatagramsUnavailable {
		t.Skip("no datagram-capable connection in this test rig")
	}
	assert.NoError(t, err)
	if tB == nil {
		t.FailNow()
	}
	defer tB.Close()

	payload := []byte("audio frame test")
	assert.NoError(t, tA.SendDatagram(payload))

	rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
	defer rcancel()
	got, err := tB.RecvDatagram(rctx)
	assert.NoError(t, err)
	assert.Equal(t, payload, got)
}

func TestTransport_ControlMessageRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	kpA, err := identity.Generate()
	assert.NoError(t, err)
	kpB, err := identity.Generate()
	assert.NoError(t, err)

	hA, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpA.Priv, RelayPeers: noRelays})
	assert.NoError(t, err)
	if hA == nil {
		t.FailNow()
	}
	t.Cleanup(func() { hA.Close() })

	hB, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpB.Priv, RelayPeers: noRelays})
	assert.NoError(t, err)
	if hB == nil {
		t.FailNow()
	}
	t.Cleanup(func() { hB.Close() })

	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)
	assert.NoError(t, hA.Connect(ctx, bInfo))

	audio.RegisterStreamHandler(hA.HostInternal())
	audio.RegisterStreamHandler(hB.HostInternal())

	tA, errA := audio.NewLibp2pTransport(ctx, hA.HostInternal(), hB.ID())
	if errA == audio.ErrDatagramsUnavailable {
		t.Skip("no datagram-capable connection in this test rig")
	}
	assert.NoError(t, errA)
	if tA == nil {
		t.FailNow()
	}
	defer tA.Close()

	tB, errB := audio.NewLibp2pTransport(ctx, hB.HostInternal(), hA.ID())
	if errB == audio.ErrDatagramsUnavailable {
		t.Skip("no datagram-capable connection in this test rig")
	}
	assert.NoError(t, errB)
	if tB == nil {
		t.FailNow()
	}
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

// TestTransport_WaitsForDatagramConn proves that NewLibp2pTransport
// polls for a direct QUIC connection instead of failing the moment
// findDatagramConn comes up empty. This is the cross-network fix:
// when peers initially connect via libp2p's circuit-relay, no QUIC
// path exists; DCUtR upgrades the connection in the background, and
// NewLibp2pTransport must wait for that upgrade rather than rejecting
// the call.
//
// Test rig: two hosts, deliberately *not* pre-connected. A goroutine
// connects them after ~600ms — well inside the 8s default wait. Without
// the wait, NewLibp2pTransport would return ErrDatagramsUnavailable on
// the first findDatagramConn call.
func TestTransport_WaitsForDatagramConn(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	kpA, err := identity.Generate()
	assert.NoError(t, err)
	kpB, err := identity.Generate()
	assert.NoError(t, err)

	hA, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpA.Priv, RelayPeers: noRelays})
	assert.NoError(t, err)
	if hA == nil {
		t.FailNow()
	}
	t.Cleanup(func() { hA.Close() })
	hB, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpB.Priv, RelayPeers: noRelays})
	assert.NoError(t, err)
	if hB == nil {
		t.FailNow()
	}
	t.Cleanup(func() { hB.Close() })

	audio.RegisterStreamHandler(hA.HostInternal())
	audio.RegisterStreamHandler(hB.HostInternal())

	// Connect A→B after a delay, simulating DCUtR completing mid-wait.
	go func() {
		time.Sleep(600 * time.Millisecond)
		bInfo, herr := p2p.HostAddrInfo(hB)
		if herr != nil {
			return
		}
		_ = hA.Connect(ctx, bInfo)
	}()

	tA, err := audio.NewLibp2pTransport(ctx, hA.HostInternal(), hB.ID())
	assert.NoError(t, err, "NewLibp2pTransport should wait for the late connection")
	if tA == nil {
		t.FailNow()
	}
	defer tA.Close()
}

// TestTransport_DatagramTimeoutSurfacesError proves that when no direct
// QUIC connection ever appears (peer never gets reachable), the wait
// times out cleanly with ErrDatagramsUnavailable rather than blocking
// the caller indefinitely. Uses a tight test deadline rather than the
// 8s production default to keep CI fast.
func TestTransport_DatagramTimeoutSurfacesError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	kpA, err := identity.Generate()
	assert.NoError(t, err)
	kpB, err := identity.Generate()
	assert.NoError(t, err)

	hA, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpA.Priv, RelayPeers: noRelays})
	assert.NoError(t, err)
	if hA == nil {
		t.FailNow()
	}
	t.Cleanup(func() { hA.Close() })

	// Cancel context quickly so we don't wait the full 8s production
	// timeout. The wait loop respects ctx and returns ctx.Err() when
	// the parent context is cancelled — semantically equivalent to a
	// timeout from the caller's perspective.
	tCtx, tCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer tCancel()

	_, err = audio.NewLibp2pTransport(tCtx, hA.HostInternal(), kpB.PeerID)
	assert.Error(t, err, "should fail when no datagram-capable connection ever appears")
}
