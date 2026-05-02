package methods_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/call"
	"opencom/internal/identity"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
)

func TestCallsSubscribe_DeliversManagerLevelStateChanges(t *testing.T) {
	t.Parallel()

	mgr := call.NewManager()
	sock := startCallsServer(t, context.Background(), func(s *ipc.Server) {
		s.Register("calls.subscribe", methods.CallsSubscribe(mgr))
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()
	sub, err := c.Subscribe(context.Background(), "calls.subscribe", nil)
	assert.NoError(t, err)
	defer sub.Close()

	// Register a fresh outbound session — should emit a state event.
	kp, _ := identity.Generate()
	_ = zap.NewNop()
	s := call.NewSession("c-1", kp.PeerID, call.Outbound, time.Now)
	mgr.Register(s)
	_ = s.ToRinging()

	select {
	case ev := <-sub.Events:
		var sc call.StateChange
		assert.NoError(t, json.Unmarshal(ev.Data, &sc))
		assert.Equal(t, "ringing", sc.State)
		assert.Equal(t, "outbound", sc.Direction)
		assert.Equal(t, "c-1", sc.SessionID)
		assert.Equal(t, kp.PeerID, peer.ID(sc.Remote))
	case <-time.After(2 * time.Second):
		t.Fatal("no state-change event delivered to subscriber")
	}
}
