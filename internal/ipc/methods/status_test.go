package methods_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/call"
	"opencom/internal/friends"
	"opencom/internal/identity"
	"opencom/internal/invite"
	"opencom/internal/ipc/methods"
)

func TestDaemonStatusSummary_AggregatesFields(t *testing.T) {
	t.Parallel()
	kp, err := identity.Generate()
	assert.NoError(t, err)
	startedAt := time.Now().UTC()

	friendsStore, err := friends.Open(filepath.Join(t.TempDir(), "friends.json"))
	assert.NoError(t, err)
	presence := friends.NewPresence(nil)
	callMgr := call.NewManager()
	inviteStore, err := invite.OpenStore(filepath.Join(t.TempDir(), "invites.json"))
	assert.NoError(t, err)

	h := methods.DaemonStatusSummary(
		"v0.1.0", kp, startedAt,
		func() []string { return []string{"/ip4/127.0.0.1/tcp/4001"} },
		func() string { return "public" },
		friendsStore, presence, callMgr, inviteStore,
	)

	out, err := h(context.Background(), nil)
	assert.NoError(t, err)

	raw, _ := json.Marshal(out)
	var got methods.DaemonStatusSummaryResult
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "v0.1.0", got.Identity.Version)
	assert.Equal(t, "public", got.Identity.Reachability)
	assert.NotNil(t, got.Friends)
	assert.NotNil(t, got.Calls)
	assert.NotNil(t, got.Invites)
}
