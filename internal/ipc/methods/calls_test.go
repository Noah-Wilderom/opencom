package methods_test

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/call"
	"opencom/internal/friends"
	"opencom/internal/identity"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
	"opencom/internal/transport/p2p"
)

type callsTestRig struct {
	eA, eB *call.Engine
	mA, mB *call.Manager
	hA, hB *p2p.Host
	pA, pB peer.ID
}

func newCallsRig(t *testing.T, ctx context.Context) *callsTestRig {
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

	return &callsTestRig{eA: eA, eB: eB, mA: mA, mB: mB, hA: hA, hB: hB, pA: hA.ID(), pB: hB.ID()}
}

func startCallsServer(t *testing.T, ctx context.Context, register func(s *ipc.Server)) string {
	t.Helper()
	skipIfWindowsNoUnixSockets(t)
	sock := filepath.Join(t.TempDir(), "test.sock")
	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)
	s := ipc.NewServer(zap.NewNop(), "test")
	if register != nil {
		register(s)
	}
	go func() { _ = s.Serve(ctx, ln) }()
	return sock
}

func TestCallsStart_DialsAndReturnsSubscription(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rig := newCallsRig(t, ctx)

	store, err := friends.Open(filepath.Join(t.TempDir(), "friends.json"))
	assert.NoError(t, err)
	pub := identity.PublicIdentity{Version: 1, Name: "Bob", PeerID: rig.pB.String(), PublicKey: "x"}
	assert.NoError(t, store.Add(friends.Friend{
		Name: "Bob", PeerID: rig.pB, PublicKey: pub.PublicKey, AddedAt: time.Now().UTC(),
	}))

	sock := startCallsServer(t, ctx, func(s *ipc.Server) {
		s.Register("calls.start", methods.CallsStart(rig.eA, rig.mA, store))
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	sub, err := c.Subscribe(context.Background(), "calls.start", methods.CallsStartParams{Target: "Bob"})
	assert.NoError(t, err)
	defer sub.Close()

	select {
	case ev := <-sub.Events:
		var sc call.StateChange
		assert.NoError(t, json.Unmarshal(ev.Data, &sc))
		assert.Equal(t, "ringing", sc.State)
	case <-time.After(2 * time.Second):
		t.Fatal("no Ringing event received")
	}
}

func TestCallsStart_UnknownFriend(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rig := newCallsRig(t, ctx)

	store, err := friends.Open(filepath.Join(t.TempDir(), "friends.json"))
	assert.NoError(t, err)

	sock := startCallsServer(t, ctx, func(s *ipc.Server) {
		s.Register("calls.start", methods.CallsStart(rig.eA, rig.mA, store))
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	err = c.Call(context.Background(), "calls.start",
		methods.CallsStartParams{Target: "Nobody"}, nil)
	assert.Error(t, err)
	var rpcErr *ipc.Error
	assert.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, ipc.ErrCodeNoSuchFriend, rpcErr.Code)
}

func TestCallsList_ReturnsActiveSessions(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)

	mA := call.NewManager()
	s := call.NewSession("c-1", kp.PeerID, call.Outbound, time.Now)
	_ = s.ToRinging()
	mA.Register(s)

	h := methods.CallsList(mA, nil)
	out, err := h(context.Background(), nil)
	assert.NoError(t, err)
	raw, _ := json.Marshal(out)
	var got methods.CallsListResult
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Len(t, got.Calls, 1)
	assert.Equal(t, "c-1", got.Calls[0].CallID)
	assert.Equal(t, "ringing", got.Calls[0].State)
	assert.Equal(t, "outbound", got.Calls[0].Direction)
}

func TestCallsAction_Hangup(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rig := newCallsRig(t, ctx)

	store, err := friends.Open(filepath.Join(t.TempDir(), "friends.json"))
	assert.NoError(t, err)
	assert.NoError(t, store.Add(friends.Friend{
		Name: "Bob", PeerID: rig.pB, PublicKey: "x", AddedAt: time.Now().UTC(),
	}))

	out, err := rig.eA.Place(ctx, rig.pB)
	assert.NoError(t, err)

	h := methods.CallsAction(rig.eA, rig.mA, nil)
	params, _ := json.Marshal(methods.CallsActionParams{
		CallID: out.ID(), Action: "hangup", Reason: "test",
	})
	_, err = h(context.Background(), params)
	assert.NoError(t, err)
	assert.Equal(t, call.StateEnded, out.State())
}

func TestCallsAction_AcceptInbound(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rig := newCallsRig(t, ctx)

	out, err := rig.eA.Place(ctx, rig.pB)
	assert.NoError(t, err)
	in := <-rig.mB.Inbound()

	h := methods.CallsAction(rig.eB, rig.mB, nil)
	params, _ := json.Marshal(methods.CallsActionParams{CallID: in.ID(), Action: "accept"})
	_, err = h(context.Background(), params)
	assert.NoError(t, err)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if out.State() == call.StateConnected {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("outbound did not reach Connected")
}

func TestCallsAction_NoSuchCall(t *testing.T) {
	t.Parallel()

	mA := call.NewManager()
	h := methods.CallsAction(nil, mA, nil)

	params, _ := json.Marshal(methods.CallsActionParams{CallID: "missing", Action: "hangup"})
	_, err := h(context.Background(), params)
	assert.Error(t, err)
	var rpcErr *ipc.Error
	assert.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, ipc.ErrCodeNoSuchCall, rpcErr.Code)
}

func TestCallsAction_InvalidAction(t *testing.T) {
	t.Parallel()

	mA := call.NewManager()
	s := call.NewSession("c-1", peer.ID("p"), call.Outbound, time.Now)
	mA.Register(s)
	h := methods.CallsAction(nil, mA, nil)

	params, _ := json.Marshal(methods.CallsActionParams{CallID: "c-1", Action: "bogus"})
	_, err := h(context.Background(), params)
	assert.Error(t, err)
	var rpcErr *ipc.Error
	assert.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, ipc.ErrCodeInvalidParams, rpcErr.Code)
}

func TestCallsAttach_NoSuchCall(t *testing.T) {
	t.Parallel()

	mA := call.NewManager()
	h := methods.CallsAttach(mA)

	params, _ := json.Marshal(methods.CallsAttachParams{CallID: "missing"})
	_, err := h(context.Background(), params)
	assert.Error(t, err)
	var rpcErr *ipc.Error
	assert.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, ipc.ErrCodeNoSuchCall, rpcErr.Code)
}

func TestCallsAttach_DeliversCurrentState(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rig := newCallsRig(t, ctx)

	out, err := rig.eA.Place(ctx, rig.pB)
	assert.NoError(t, err)
	_ = out

	sock := startCallsServer(t, ctx, func(s *ipc.Server) {
		s.Register("calls.attach", methods.CallsAttach(rig.mA))
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	sub, err := c.Subscribe(context.Background(), "calls.attach",
		methods.CallsAttachParams{CallID: out.ID()})
	assert.NoError(t, err)
	defer sub.Close()

	select {
	case ev := <-sub.Events:
		var sc call.StateChange
		assert.NoError(t, json.Unmarshal(ev.Data, &sc))
		assert.Equal(t, "ringing", sc.State)
	case <-time.After(2 * time.Second):
		t.Fatal("no current-state event delivered on attach")
	}
}

func TestCallsAttach_ForwardsTransitions(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rig := newCallsRig(t, ctx)

	out, err := rig.eA.Place(ctx, rig.pB)
	assert.NoError(t, err)
	in := <-rig.mB.Inbound()

	// Caller's daemon attaches to the outbound session.
	sock := startCallsServer(t, ctx, func(s *ipc.Server) {
		s.Register("calls.attach", methods.CallsAttach(rig.mA))
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	sub, err := c.Subscribe(context.Background(), "calls.attach",
		methods.CallsAttachParams{CallID: out.ID()})
	assert.NoError(t, err)
	defer sub.Close()

	// First event: synthetic Ringing.
	select {
	case ev := <-sub.Events:
		var sc call.StateChange
		assert.NoError(t, json.Unmarshal(ev.Data, &sc))
		assert.Equal(t, "ringing", sc.State)
	case <-time.After(2 * time.Second):
		t.Fatal("no initial event")
	}

	// Bob accepts → Connecting then Connected events flow to subscriber.
	assert.NoError(t, rig.eB.Accept(in))

	seenConnecting := false
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-sub.Events:
			var sc call.StateChange
			assert.NoError(t, json.Unmarshal(ev.Data, &sc))
			switch sc.State {
			case "connecting":
				seenConnecting = true
			case "connected":
				assert.True(t, seenConnecting, "should have seen connecting before connected")
				return
			case "ringing":
				// Allowed only if the synthetic dedup didn't fire — should not happen.
				t.Fatal("duplicate ringing event leaked through dedup")
			}
		case <-deadline:
			t.Fatalf("did not see connected; seenConnecting=%v", seenConnecting)
		}
	}
}
