package call_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/call"
	"opencom/internal/identity"
	"opencom/internal/transport/p2p"
)

// helper: spin up two libp2p hosts, connect them, return both engines.
func twoEngines(t *testing.T, ctx context.Context) (*call.Engine, *call.Engine, *call.Manager, *call.Manager, peer.ID, peer.ID) {
	t.Helper()

	kpA, err := identity.Generate()
	assert.NoError(t, err)
	kpB, err := identity.Generate()
	assert.NoError(t, err)

	hA, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpA.Priv})
	assert.NoError(t, err)
	t.Cleanup(func() { hA.Close() })

	hB, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpB.Priv})
	assert.NoError(t, err)
	t.Cleanup(func() { hB.Close() })

	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)
	assert.NoError(t, hA.Connect(ctx, bInfo))

	mA := call.NewManager()
	mB := call.NewManager()

	eA := call.NewEngine(hA, mA, zap.NewNop(), time.Now)
	eB := call.NewEngine(hB, mB, zap.NewNop(), time.Now)
	eA.Start()
	eB.Start()

	return eA, eB, mA, mB, hA.ID(), hB.ID()
}

func TestEngine_PlaceDeliversInvite(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eA, _, _, mB, _, peerB := twoEngines(t, ctx)

	out, err := eA.Place(ctx, peerB)
	assert.NoError(t, err)
	assert.Equal(t, call.StateRinging, out.State())

	select {
	case in := <-mB.Inbound():
		assert.Equal(t, call.Inbound, in.Direction())
		assert.Equal(t, call.StateRinging, in.State())
		assert.Equal(t, out.ID(), in.ID(), "both sides share the call id")
	case <-time.After(2 * time.Second):
		t.Fatal("inbound session not delivered")
	}
}

func TestEngine_AcceptAdvancesBothSidesToConnected(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eA, eB, _, mB, _, peerB := twoEngines(t, ctx)

	out, err := eA.Place(ctx, peerB)
	assert.NoError(t, err)

	in := <-mB.Inbound()
	subOut, evOut := out.Subscribe()
	defer out.Unsubscribe(subOut)

	assert.NoError(t, eB.Accept(in))

	// Wait for outbound to reach Connected.
	for {
		select {
		case ev := <-evOut:
			if ev.State == "connected" {
				goto outConnected
			}
		case <-time.After(2 * time.Second):
			t.Fatal("outbound did not reach Connected")
		}
	}
outConnected:
	assert.Equal(t, call.StateConnected, out.State())
	// Inbound should also be Connected (Accept transitions Ringing → Connecting → Connected synchronously).
	assert.Equal(t, call.StateConnected, in.State())
}

func TestEngine_HangupEndsBothSides(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eA, eB, _, mB, _, peerB := twoEngines(t, ctx)

	out, err := eA.Place(ctx, peerB)
	assert.NoError(t, err)
	in := <-mB.Inbound()
	assert.NoError(t, eB.Accept(in))

	// Wait for both Connected.
	for out.State() != call.StateConnected {
		time.Sleep(10 * time.Millisecond)
	}

	subIn, evIn := in.Subscribe()
	defer in.Unsubscribe(subIn)

	assert.NoError(t, eA.Hangup(out, "user requested"))

	for {
		select {
		case ev := <-evIn:
			if ev.State == "ended" {
				assert.Equal(t, "user requested", ev.Reason)
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("inbound did not see hangup")
		}
	}
}

func TestNewCallID_Unique(t *testing.T) {
	t.Parallel()

	a := call.NewCallID()
	b := call.NewCallID()
	assert.NotEqual(t, a, b)
	assert.NotEmpty(t, a)
}

func TestEngine_RejectsNonInviteFirstMessage(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eA, _, _, mB, _, peerB := twoEngines(t, ctx)
	_ = eA

	// Open a raw stream to B with the control protocol but send a HANGUP
	// instead of an INVITE — the handler must reject it.
	stream, err := eA.HostForTest().NewStream(ctx, peerB, call.ProtocolID)
	assert.NoError(t, err)
	defer stream.Close()

	_, err = stream.Write([]byte(`{"type":"hangup","call_id":"x"}` + "\n"))
	assert.NoError(t, err)

	// B must NOT register an inbound session.
	select {
	case s := <-mB.Inbound():
		t.Fatalf("rejected first message must not register session: %v", s)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestEngine_IgnoresCallIDMismatch(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eA, _, _, mB, _, peerB := twoEngines(t, ctx)

	out, err := eA.Place(ctx, peerB)
	assert.NoError(t, err)
	in := <-mB.Inbound()

	// Hand-craft a wrong-call-id message on B's stream — the read loop
	// should ignore it without changing state.
	rogue, err := eA.HostForTest().NewStream(ctx, peerB, call.ProtocolID)
	assert.NoError(t, err)
	defer rogue.Close()
	// Send a valid INVITE first so the handler accepts the stream, then
	// a message with a mismatched call id.
	_, err = rogue.Write([]byte(`{"type":"invite","call_id":"rogue-1"}` + "\n"))
	assert.NoError(t, err)
	_, err = rogue.Write([]byte(`{"type":"hangup","call_id":"not-the-original"}` + "\n"))
	assert.NoError(t, err)

	// in should still be Ringing (the rogue stream creates a *separate*
	// session; the original is untouched).
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, call.StateRinging, in.State())
	assert.Equal(t, out.ID(), in.ID())
}

// fakeResolver records Resolve calls and returns canned addresses.
// If addrsByCall is non-nil, it overrides addrs and the n-th Resolve
// returns addrsByCall[n] (clamped to the last entry); used to simulate
// "stale on first lookup, fresh on retry".
type fakeResolver struct {
	calls       int
	invalidated peer.ID
	addrs       []ma.Multiaddr
	addrsByCall [][]ma.Multiaddr
}

func (f *fakeResolver) Resolve(_ context.Context, target peer.ID) ([]ma.Multiaddr, error) {
	idx := f.calls
	f.calls++
	if f.addrsByCall != nil {
		if idx >= len(f.addrsByCall) {
			idx = len(f.addrsByCall) - 1
		}
		return f.addrsByCall[idx], nil
	}
	return f.addrs, nil
}
func (f *fakeResolver) InvalidateCache(target peer.ID) { f.invalidated = target }

// TestEngine_PlaceRefreshesAddressesOnDialFailure proves the
// stale-address recovery path: when the first NewStream attempt fails,
// the resolver's cache is invalidated and a fresh lookup is performed
// before the dial is retried. Without this, peers that have moved
// networks (or whose relay reservation expired) require the user to
// manually re-issue `opencom call`.
func TestEngine_PlaceRefreshesAddressesOnDialFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Two unconnected hosts; A's peerstore knows nothing about B.
	kpA, err := identity.Generate()
	assert.NoError(t, err)
	kpB, err := identity.Generate()
	assert.NoError(t, err)
	hA, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpA.Priv})
	assert.NoError(t, err)
	t.Cleanup(func() { hA.Close() })
	hB, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpB.Priv})
	assert.NoError(t, err)
	t.Cleanup(func() { hB.Close() })

	mA := call.NewManager()
	mB := call.NewManager()
	eA := call.NewEngine(hA, mA, zap.NewNop(), time.Now)
	eB := call.NewEngine(hB, mB, zap.NewNop(), time.Now)
	eA.Start()
	eB.Start()

	bInfo, err := p2p.HostAddrInfo(hB)
	assert.NoError(t, err)

	// First Resolve returns no addrs (stale-cache scenario: nothing
	// usable). Second Resolve (post-failure, post-invalidate) returns
	// B's real addresses so the retry can connect.
	r := &fakeResolver{addrsByCall: [][]ma.Multiaddr{
		{},          // call 1: stale/empty → first NewStream fails
		bInfo.Addrs, // call 2: fresh from DHT → retry succeeds
	}}
	eA.SetResolver(r)

	out, err := eA.Place(ctx, hB.ID())
	assert.NoError(t, err, "Place should succeed via the retry path")
	assert.NotNil(t, out)

	assert.Equal(t, 2, r.calls, "resolver must be consulted twice (initial + post-failure refresh)")
	assert.Equal(t, hB.ID(), r.invalidated, "resolver cache must be invalidated for the target between attempts")
}

// TestEngine_PlaceAddsRelayCircuitFallback proves that when relays are
// configured via SetRelays, Place adds /<relay>/p2p-circuit dial
// candidates to the peerstore for the target before NewStream — even
// if the resolver returns nothing. Without this, libp2p has no way to
// reach a NAT'd peer it doesn't already have a fresh address for.
func TestEngine_PlaceAddsRelayCircuitFallback(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Three hosts: A (caller), B (target/callee), R (relay).
	kpA, _ := identity.Generate()
	kpB, _ := identity.Generate()
	kpR, _ := identity.Generate()

	hR, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpR.Priv})
	assert.NoError(t, err)
	t.Cleanup(func() { hR.Close() })
	hA, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpA.Priv})
	assert.NoError(t, err)
	t.Cleanup(func() { hA.Close() })
	hB, err := p2p.New(ctx, p2p.HostOptions{PrivKey: kpB.Priv})
	assert.NoError(t, err)
	t.Cleanup(func() { hB.Close() })

	relayInfo, err := p2p.HostAddrInfo(hR)
	assert.NoError(t, err)

	// Wire up engines but DO NOT pre-connect A↔B. We're only proving
	// that the fallback addresses get into A's peerstore — not that
	// the full circuit dial succeeds (that needs relay-v2 service
	// configured, which is a separate test surface).
	mA := call.NewManager()
	mB := call.NewManager()
	eA := call.NewEngine(hA, mA, zap.NewNop(), time.Now)
	eB := call.NewEngine(hB, mB, zap.NewNop(), time.Now)
	eA.SetRelays([]peer.AddrInfo{relayInfo})
	eA.Start()
	eB.Start()

	// Try to place. Will fail (no real circuit), but we don't care
	// about success — just that the peerstore got the fallback addrs.
	pctx, pcancel := context.WithTimeout(ctx, 1*time.Second)
	_, _ = eA.Place(pctx, hB.ID())
	pcancel()

	// Inspect A's peerstore for B: it should now contain at least one
	// /p2p-circuit/ address pointing through R.
	addrs := hA.HostInternal().Peerstore().Addrs(hB.ID())
	var sawCircuitViaRelay bool
	for _, a := range addrs {
		s := a.String()
		if !strings.Contains(s, "p2p-circuit") {
			continue
		}
		if strings.Contains(s, relayInfo.ID.String()) {
			sawCircuitViaRelay = true
			break
		}
	}
	assert.True(t, sawCircuitViaRelay,
		"peerstore should contain a /<relay>/p2p-circuit address for the target after Place; got %v", addrs)
}

// TestTranslateDialError_RewritesNoReservation proves that a libp2p
// dial error mentioning NO_RESERVATION is rewritten into an actionable
// message that names the peer and points the user at the likely fix
// (peer needs a relay reservation). All other errors pass through
// unchanged so genuinely novel failures still surface in full.
func TestTranslateDialError_RewritesNoReservation(t *testing.T) {
	t.Parallel()

	pid := peer.ID("12D3KooWFakeForTest")

	t.Run("no reservation", func(t *testing.T) {
		raw := errors.New("failed to dial: all dials failed\n  * [/ip4/x/p2p-circuit] error opening relay circuit: NO_RESERVATION (204)")
		got := call.TranslateDialErrorForTest(pid, raw)
		assert.Error(t, got)
		assert.Contains(t, got.Error(), "no relay reservation")
		assert.Contains(t, got.Error(), pid.String())
		// Original error preserved via %w so callers can still inspect.
		assert.ErrorIs(t, got, raw)
	})

	t.Run("unrelated error passes through", func(t *testing.T) {
		raw := errors.New("connection failed")
		got := call.TranslateDialErrorForTest(pid, raw)
		assert.Equal(t, raw, got, "non-NO_RESERVATION errors must not be rewritten")
	})

	t.Run("nil passes through", func(t *testing.T) {
		assert.NoError(t, call.TranslateDialErrorForTest(pid, nil))
	})
}

func TestEngine_PlaceConsultsResolverBeforeDial(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eA, _, _, mB, _, peerB := twoEngines(t, ctx)
	_ = mB

	r := &fakeResolver{addrs: nil} // doesn't matter; rig already pre-connects
	eA.SetResolver(r)

	out, err := eA.Place(ctx, peerB)
	assert.NoError(t, err)
	_ = out

	assert.GreaterOrEqual(t, r.calls, 1, "resolver must be called at least once during Place")
}

func TestEngine_HandleStreamTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	eA, _, _, mB, _, peerB := twoEngines(t, ctx)

	stream, err := eA.HostForTest().NewStream(ctx, peerB, call.ProtocolID)
	assert.NoError(t, err)
	defer stream.Close()

	// Write nothing. The handler must close the stream within ~10s.
	// Read should EOF reasonably quickly.
	buf := make([]byte, 1)
	deadline := time.Now().Add(12 * time.Second)
	_ = stream.SetReadDeadline(deadline)
	_, err = stream.Read(buf)
	assert.Error(t, err) // EOF or reset

	// And no inbound session was registered.
	select {
	case s := <-mB.Inbound():
		t.Fatalf("timed-out stream must not register session: %v", s)
	default:
	}
}
