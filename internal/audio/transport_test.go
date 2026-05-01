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
