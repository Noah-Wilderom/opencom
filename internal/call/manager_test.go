package call_test

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"

	"opencom/internal/call"
)

func TestManager_RegisterGetList(t *testing.T) {
	t.Parallel()

	m := call.NewManager()
	a := call.NewSession("a", peer.ID("p1"), call.Outbound, time.Now)
	b := call.NewSession("b", peer.ID("p2"), call.Inbound, time.Now)

	m.Register(a)
	m.Register(b)

	got, ok := m.Get("a")
	assert.True(t, ok)
	assert.Equal(t, a, got)

	all := m.List()
	assert.Len(t, all, 2)
	assert.Equal(t, "a", all[0].ID())
	assert.Equal(t, "b", all[1].ID())
}

func TestManager_GetMissing(t *testing.T) {
	t.Parallel()

	m := call.NewManager()
	_, ok := m.Get("missing")
	assert.False(t, ok)
}

func TestManager_Remove(t *testing.T) {
	t.Parallel()

	m := call.NewManager()
	s := call.NewSession("a", peer.ID("p"), call.Outbound, time.Now)
	m.Register(s)
	m.Remove("a")

	_, ok := m.Get("a")
	assert.False(t, ok)
	assert.Empty(t, m.List())
}

func TestManager_InboundChannelDeliversIncomingCalls(t *testing.T) {
	t.Parallel()

	m := call.NewManager()
	s := call.NewSession("a", peer.ID("p"), call.Inbound, time.Now)

	go func() {
		time.Sleep(10 * time.Millisecond)
		m.Register(s)
	}()

	select {
	case got := <-m.Inbound():
		assert.Equal(t, s, got)
	case <-time.After(time.Second):
		t.Fatal("no inbound delivered")
	}
}

func TestManager_OutboundDoesNotFireInbound(t *testing.T) {
	t.Parallel()

	m := call.NewManager()
	s := call.NewSession("a", peer.ID("p"), call.Outbound, time.Now)
	m.Register(s)

	// Register is synchronous and the inbound publish (if any) happens
	// before it returns, so a non-blocking receive here is deterministic.
	select {
	case got := <-m.Inbound():
		t.Fatalf("outbound should not deliver to Inbound channel: %v", got)
	default:
	}
}

func TestManager_RegisterIdempotent(t *testing.T) {
	t.Parallel()

	m := call.NewManager()
	s := call.NewSession("a", peer.ID("p"), call.Outbound, time.Now)
	m.Register(s)
	m.Register(s)

	assert.Len(t, m.List(), 1)
}
