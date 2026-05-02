package notify_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"

	"opencom/internal/call"
	"opencom/internal/friends"
	"opencom/internal/notify"
)

// fakeNamer satisfies FriendNamer with an in-memory map keyed by peer.ID.
type fakeNamer struct{ byID map[peer.ID]friends.Friend }

func (f fakeNamer) GetByPeerID(id peer.ID) (friends.Friend, bool) {
	x, ok := f.byID[id]
	return x, ok
}

// recordingNotifier captures every Notify call for assertion.
type recordingNotifier struct {
	mu      sync.Mutex
	titles  []string
	bodies  []string
}

func (r *recordingNotifier) Notify(title, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.titles = append(r.titles, title)
	r.bodies = append(r.bodies, body)
}

func (r *recordingNotifier) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := append([]string(nil), r.titles...)
	b := append([]string(nil), r.bodies...)
	return t, b
}

// fakeSource is a hand-rolled CallSource that lets the test push
// StateChange events on demand without standing up a real call.Manager.
type fakeSource struct{ ch chan call.StateChange }

func newFakeSource() *fakeSource              { return &fakeSource{ch: make(chan call.StateChange, 8)} }
func (f *fakeSource) SubscribeStateChanges() <-chan call.StateChange { return f.ch }
func (f *fakeSource) UnsubscribeStateChanges(_ <-chan call.StateChange) {}

func TestFormatNotification_KnownFriend(t *testing.T) {
	t.Parallel()
	id := peer.ID("12D3KooWAlice")
	names := fakeNamer{byID: map[peer.ID]friends.Friend{id: {Name: "Alice", PeerID: id}}}

	cases := []struct {
		state, dir, reason  string
		wantTitle, wantBody string
	}{
		{"ringing", "inbound", "", "Incoming call", "from Alice"},
		{"ringing", "outbound", "", "Calling…", "Alice"},
		{"connected", "outbound", "", "Call connected", "Alice"},
		{"ended", "outbound", "", "Call ended", "Alice"},
		{"ended", "inbound", "user requested", "Call ended", "Alice — user requested"},
		{"connecting", "outbound", "", "", ""}, // unmapped → no notification
	}
	for _, c := range cases {
		ev := call.StateChange{State: c.state, Direction: c.dir, Remote: id, Reason: c.reason}
		title, body := notify.FormatNotificationForTest(ev, names)
		assert.Equal(t, c.wantTitle, title, "title for %s/%s", c.state, c.dir)
		assert.Equal(t, c.wantBody, body, "body for %s/%s", c.state, c.dir)
	}
}

func TestFormatNotification_UnknownPeerFallsBackToShortID(t *testing.T) {
	t.Parallel()
	id := peer.ID("12D3KooWStrangerSomeLongPeerIDValue")
	names := fakeNamer{byID: map[peer.ID]friends.Friend{}}

	ev := call.StateChange{State: "ringing", Direction: "inbound", Remote: id}
	title, body := notify.FormatNotificationForTest(ev, names)
	assert.Equal(t, "Incoming call", title)
	assert.Contains(t, body, "from ")
	assert.Contains(t, body, "…", "long peer IDs should be elided with an ellipsis")
}

func TestWatchCalls_FiresOnStateChange(t *testing.T) {
	t.Parallel()
	id := peer.ID("12D3KooWBob")
	names := fakeNamer{byID: map[peer.ID]friends.Friend{id: {Name: "Bob", PeerID: id}}}
	src := newFakeSource()
	rec := &recordingNotifier{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		notify.WatchCalls(ctx, src, names, rec, nil)
		close(done)
	}()

	src.ch <- call.StateChange{State: "ringing", Direction: "outbound", Remote: id}
	src.ch <- call.StateChange{State: "connected", Direction: "outbound", Remote: id}
	src.ch <- call.StateChange{State: "ended", Direction: "outbound", Remote: id, Reason: "peer hung up"}

	deadline := time.After(2 * time.Second)
	for {
		titles, _ := rec.snapshot()
		if len(titles) >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only got %d notifications, expected >=3", len(titles))
		case <-time.After(20 * time.Millisecond):
		}
	}

	titles, bodies := rec.snapshot()
	assert.Equal(t, []string{"Calling…", "Call connected", "Call ended"}, titles)
	assert.Equal(t, "Bob", bodies[0])
	assert.Equal(t, "Bob", bodies[1])
	assert.Equal(t, "Bob — peer hung up", bodies[2])

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WatchCalls did not return after ctx cancel")
	}
}

func TestDisabled_NoOps(t *testing.T) {
	t.Parallel()
	// Just verify it satisfies the interface and doesn't panic.
	var n notify.Notifier = notify.Disabled{}
	n.Notify("anything", "everything")
}
