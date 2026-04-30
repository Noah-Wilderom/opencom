package call_test

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"

	"opencom/internal/call"
)

func TestState_String(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "idle", call.StateIdle.String())
	assert.Equal(t, "ringing", call.StateRinging.String())
	assert.Equal(t, "connecting", call.StateConnecting.String())
	assert.Equal(t, "connected", call.StateConnected.String())
	assert.Equal(t, "ended", call.StateEnded.String())
}

func TestSession_LinearTransitions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	s := call.NewSession("c-1", peer.ID("12D3KooWAlice"), call.Outbound, func() time.Time { return now })

	assert.Equal(t, call.StateIdle, s.State())
	assert.NoError(t, s.ToRinging())
	assert.Equal(t, call.StateRinging, s.State())
	assert.NoError(t, s.ToConnecting())
	assert.NoError(t, s.ToConnected())
	assert.NoError(t, s.End("user requested"))
	assert.Equal(t, call.StateEnded, s.State())
	assert.Equal(t, "user requested", s.Reason())
}

func TestSession_BackwardsTransitionRejected(t *testing.T) {
	t.Parallel()

	s := call.NewSession("c-1", peer.ID("12D3KooWAlice"), call.Outbound, time.Now)
	assert.NoError(t, s.ToRinging())
	assert.NoError(t, s.ToConnecting())

	err := s.ToRinging()
	assert.Error(t, err)
}

func TestSession_EndFromAnyState(t *testing.T) {
	t.Parallel()

	for _, prep := range []func(*call.Session){
		func(s *call.Session) {}, // Idle
		func(s *call.Session) { _ = s.ToRinging() },
		func(s *call.Session) { _ = s.ToRinging(); _ = s.ToConnecting() },
		func(s *call.Session) { _ = s.ToRinging(); _ = s.ToConnecting(); _ = s.ToConnected() },
	} {
		s := call.NewSession("c-1", peer.ID("p"), call.Outbound, time.Now)
		prep(s)
		assert.NoError(t, s.End("test"))
		assert.Equal(t, call.StateEnded, s.State())
	}
}

func TestSession_EndIsIdempotent(t *testing.T) {
	t.Parallel()

	s := call.NewSession("c-1", peer.ID("p"), call.Outbound, time.Now)
	assert.NoError(t, s.End("first"))
	assert.NoError(t, s.End("second"))
	// First reason is preserved.
	assert.Equal(t, "first", s.Reason())
}

func TestSession_SubscribeReceivesTransitions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	s := call.NewSession("c-1", peer.ID("p"), call.Outbound, func() time.Time { return now })
	subID, events := s.Subscribe()
	defer s.Unsubscribe(subID)

	assert.NoError(t, s.ToRinging())

	select {
	case ev := <-events:
		assert.Equal(t, "c-1", ev.SessionID)
		assert.Equal(t, "ringing", ev.State)
		assert.Equal(t, "outbound", ev.Direction)
		assert.Equal(t, now, ev.Time)
		assert.Equal(t, peer.ID("p"), ev.Remote)
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}
}

func TestSession_UnsubscribeCloseChannel(t *testing.T) {
	t.Parallel()

	s := call.NewSession("c-1", peer.ID("p"), call.Outbound, time.Now)
	subID, events := s.Subscribe()
	s.Unsubscribe(subID)

	_, ok := <-events
	assert.False(t, ok, "channel should be closed")
}

func TestSession_ForwardSkipRejected(t *testing.T) {
	t.Parallel()

	// Idle -> Connecting (skips Ringing) must error.
	s := call.NewSession("c-1", peer.ID("p"), call.Outbound, time.Now)
	assert.Error(t, s.ToConnecting())
	assert.Equal(t, call.StateIdle, s.State())

	// Idle -> Connected (skips Ringing and Connecting) must error.
	s2 := call.NewSession("c-2", peer.ID("p"), call.Outbound, time.Now)
	assert.Error(t, s2.ToConnected())
	assert.Equal(t, call.StateIdle, s2.State())

	// Ringing -> Connected (skips Connecting) must error.
	s3 := call.NewSession("c-3", peer.ID("p"), call.Outbound, time.Now)
	assert.NoError(t, s3.ToRinging())
	assert.Error(t, s3.ToConnected())
	assert.Equal(t, call.StateRinging, s3.State())
}

func TestSession_EndEventCarriesReason(t *testing.T) {
	t.Parallel()

	s := call.NewSession("c-1", peer.ID("p"), call.Outbound, time.Now)
	subID, events := s.Subscribe()
	defer s.Unsubscribe(subID)

	assert.NoError(t, s.End("user requested"))

	select {
	case ev := <-events:
		assert.Equal(t, "ended", ev.State)
		assert.Equal(t, "user requested", ev.Reason)
	case <-time.After(time.Second):
		t.Fatal("no event received")
	}
}

func TestSession_DirectionString(t *testing.T) {
	t.Parallel()

	out := call.NewSession("a", peer.ID("p"), call.Outbound, time.Now)
	in := call.NewSession("b", peer.ID("p"), call.Inbound, time.Now)
	subOut, evOut := out.Subscribe()
	defer out.Unsubscribe(subOut)
	subIn, evIn := in.Subscribe()
	defer in.Unsubscribe(subIn)

	_ = out.ToRinging()
	_ = in.ToRinging()

	o := <-evOut
	i := <-evIn
	assert.Equal(t, "outbound", o.Direction)
	assert.Equal(t, "inbound", i.Direction)
}
