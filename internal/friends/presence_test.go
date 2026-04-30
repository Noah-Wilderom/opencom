package friends_test

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"

	"opencom/internal/friends"
)

func TestPresence_MarkOnlineAndQuery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	p := friends.NewPresence(func() time.Time { return now })

	id := peer.ID("12D3KooWAlice")
	assert.False(t, p.IsOnline(id))

	p.MarkOnline(id)
	assert.True(t, p.IsOnline(id))
	assert.Equal(t, now, p.LastSeen(id))
}

func TestPresence_MarkOfflineKeepsLastSeen(t *testing.T) {
	t.Parallel()

	calls := 0
	clk := func() time.Time {
		calls++
		return time.Date(2026, 5, 1, 12, 0, calls, 0, time.UTC)
	}
	p := friends.NewPresence(clk)
	id := peer.ID("12D3KooWAlice")

	p.MarkOnline(id)
	online := p.LastSeen(id)
	p.MarkOffline(id)
	assert.False(t, p.IsOnline(id))
	assert.True(t, p.LastSeen(id).After(online), "MarkOffline updates LastSeen")
}

func TestPresence_SubscribeReceivesEvents(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	p := friends.NewPresence(func() time.Time { return now })

	subID, events := p.Subscribe()
	defer p.Unsubscribe(subID)

	id := peer.ID("12D3KooWAlice")
	p.MarkOnline(id)

	select {
	case ev := <-events:
		assert.Equal(t, id, ev.PeerID)
		assert.True(t, ev.Online)
		assert.Equal(t, now, ev.Time)
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}
}

func TestPresence_UnsubscribeStopsEvents(t *testing.T) {
	t.Parallel()

	p := friends.NewPresence(time.Now)
	subID, events := p.Subscribe()
	p.Unsubscribe(subID)

	p.MarkOnline(peer.ID("12D3KooWAlice"))

	select {
	case ev, ok := <-events:
		// Channel may be closed (ok=false) — that's fine; receiving an
		// event would be the bug.
		if ok {
			t.Fatalf("received event after unsubscribe: %+v", ev)
		}
	case <-time.After(50 * time.Millisecond):
		// No event delivered — also acceptable.
	}
}

func TestPresence_RedundantMarksAreIdempotent(t *testing.T) {
	t.Parallel()

	p := friends.NewPresence(time.Now)
	subID, events := p.Subscribe()
	defer p.Unsubscribe(subID)

	id := peer.ID("12D3KooWAlice")
	p.MarkOnline(id)
	p.MarkOnline(id)
	p.MarkOnline(id)

	count := 0
	deadline := time.After(100 * time.Millisecond)
loop:
	for {
		select {
		case <-events:
			count++
		case <-deadline:
			break loop
		}
	}
	assert.Equal(t, 1, count, "redundant MarkOnline should fire only once")
}

func TestPresence_MarkOfflineUnknownPeerIsNoop(t *testing.T) {
	t.Parallel()

	p := friends.NewPresence(time.Now)
	subID, events := p.Subscribe()
	defer p.Unsubscribe(subID)

	id := peer.ID("12D3KooWStranger")
	p.MarkOffline(id)

	assert.False(t, p.IsOnline(id))
	assert.True(t, p.LastSeen(id).IsZero(), "LastSeen must be zero for never-observed peer")

	select {
	case ev := <-events:
		t.Fatalf("MarkOffline on unknown peer should not fire an event: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}
