package audio_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/audio"
	"opencom/internal/call"
)

// fakeCallManager implements audio.CallStateSource for tests.
type fakeCallManager struct {
	events chan call.StateChange
}

func (f *fakeCallManager) SubscribeStateChanges() <-chan call.StateChange {
	return f.events
}

func (f *fakeCallManager) UnsubscribeStateChanges(ch <-chan call.StateChange) {
	// tests don't need real cleanup
}

func TestManager_SpawnsSessionOnConnected(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	fcm := &fakeCallManager{events: make(chan call.StateChange, 4)}
	m, err := audio.NewManager(audio.ManagerOptions{
		Calls: fcm,
		Log:   zap.NewNop(),
	})
	assert.NoError(t, err)
	go m.Start(ctx)
	defer m.Stop()

	fcm.events <- call.StateChange{SessionID: "call-1", State: "connected"}
	time.Sleep(100 * time.Millisecond)
	_, ok := m.Stats("call-1")
	assert.True(t, ok, "session should exist for connected call")

	fcm.events <- call.StateChange{SessionID: "call-1", State: "ended"}
	time.Sleep(100 * time.Millisecond)
	_, ok = m.Stats("call-1")
	assert.False(t, ok, "session should be torn down on ended")
}

func TestManager_StopClosesAllSessions(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fcm := &fakeCallManager{events: make(chan call.StateChange, 4)}
	m, err := audio.NewManager(audio.ManagerOptions{
		Calls: fcm,
		Log:   zap.NewNop(),
	})
	assert.NoError(t, err)
	go m.Start(ctx)

	fcm.events <- call.StateChange{SessionID: "call-1", State: "connected"}
	fcm.events <- call.StateChange{SessionID: "call-2", State: "connected"}
	time.Sleep(100 * time.Millisecond)

	m.Stop()
	_, ok := m.Stats("call-1")
	assert.False(t, ok)
	_, ok = m.Stats("call-2")
	assert.False(t, ok)
}
