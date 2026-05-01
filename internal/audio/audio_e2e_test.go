package audio_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/audio"
	"opencom/internal/identity"
	"opencom/internal/transport/p2p"
)

// TestE2E_TwoSessionsExchangeAudio drives a sine wave through one
// session's "mic" and asserts the other session's "speaker" hears it.
// Uses two real libp2p hosts (in-process, on QUIC localhost) so the
// real Transport (datagrams) and the real pipeline run end-to-end.
func TestE2E_TwoSessionsExchangeAudio(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test requires libp2p hosts; -short skips")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	kpA, err := identity.Generate()
	assert.NoError(t, err)
	kpB, err := identity.Generate()
	assert.NoError(t, err)

	// Force QUIC-only loopback listens so libp2p picks the QUIC
	// transport (which carries datagrams). With the default TCP+QUIC
	// listen set, the test rig sometimes lands on TCP and
	// findDatagramConn returns ErrDatagramsUnavailable.
	quicAddr, err := ma.NewMultiaddr("/ip4/127.0.0.1/udp/0/quic-v1")
	assert.NoError(t, err)
	listen := []ma.Multiaddr{quicAddr}

	hA, err := p2p.New(ctx, p2p.HostOptions{
		PrivKey:     kpA.Priv,
		ListenAddrs: listen,
		RelayPeers:  noRelays,
	})
	assert.NoError(t, err)
	if hA == nil {
		t.FailNow()
	}
	t.Cleanup(func() { hA.Close() })

	hB, err := p2p.New(ctx, p2p.HostOptions{
		PrivKey:     kpB.Priv,
		ListenAddrs: listen,
		RelayPeers:  noRelays,
	})
	assert.NoError(t, err)
	if hB == nil {
		t.FailNow()
	}
	t.Cleanup(func() { hB.Close() })

	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)
	assert.NoError(t, hA.Connect(ctx, bInfo))

	// Register the audio-control stream handler on both hosts BEFORE
	// constructing transports so each side can accept the inbound
	// control stream the peer opens during NewLibp2pTransport.
	audio.RegisterStreamHandler(hA.HostInternal())
	audio.RegisterStreamHandler(hB.HostInternal())

	// Capture/playback are fakes (no real hardware in CI).
	frames := make([][]int16, 100)
	for i := range frames {
		frames[i] = makeSine(440, audio.FrameSize)
	}
	srcA := &fakeSource{frames: frames}
	sinkA := &fakeSink{}
	srcB := &fakeSource{frames: nil}
	sinkB := &fakeSink{}

	// Build sessions using the dep-injected constructor with real
	// libp2p transports.
	tA, err := audio.NewLibp2pTransport(ctx, hA.HostInternal(), hB.ID())
	if errors.Is(err, audio.ErrDatagramsUnavailable) {
		t.Skip("loopback connection has no datagrams; rig needs QUIC")
	}
	assert.NoError(t, err)
	if tA == nil {
		t.FailNow()
	}

	tB, err := audio.NewLibp2pTransport(ctx, hB.HostInternal(), hA.ID())
	if errors.Is(err, audio.ErrDatagramsUnavailable) {
		t.Skip("loopback connection has no datagrams; rig needs QUIC")
	}
	assert.NoError(t, err)
	if tB == nil {
		t.FailNow()
	}

	sessA, err := audio.NewSessionWithDeps(ctx, audio.SessionOptions{
		CallID: "e2e", Bitrate: 48000, JitterTargetMs: 60, JitterMaxMs: 200,
		AECEnabled: false, Log: zap.NewNop(),
	}, srcA, sinkA, tA)
	assert.NoError(t, err)
	if sessA == nil {
		t.FailNow()
	}
	t.Cleanup(sessA.Close)

	sessB, err := audio.NewSessionWithDeps(ctx, audio.SessionOptions{
		CallID: "e2e", Bitrate: 48000, JitterTargetMs: 60, JitterMaxMs: 200,
		AECEnabled: false, Log: zap.NewNop(),
	}, srcB, sinkB, tB)
	assert.NoError(t, err)
	if sessB == nil {
		t.FailNow()
	}
	t.Cleanup(sessB.Close)

	// Let audio flow long enough that the encoder ramp + jitter buffer
	// fill (~200ms) plus 30 frames at 20ms each (600ms) clear with
	// margin on slow CI.
	time.Sleep(1500 * time.Millisecond)

	sinkB.mu.Lock()
	got := append([][]int16(nil), sinkB.frames...)
	sinkB.mu.Unlock()
	assert.GreaterOrEqual(t, len(got), 30, "B should have received >=30 frames")
	// Energy check: at least some non-silent frame.
	var seenEnergy bool
	for _, f := range got {
		for _, s := range f {
			if s != 0 {
				seenEnergy = true
				break
			}
		}
		if seenEnergy {
			break
		}
	}
	assert.True(t, seenEnergy, "B should have heard the sine wave")
}
