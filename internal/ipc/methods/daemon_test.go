package methods_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/identity"
	"opencom/internal/ipc/methods"
)

func TestDaemonStatus_PopulatesAllFields(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)
	startedAt := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)

	h := methods.DaemonStatus("v0.1.0", kp, startedAt, nil, nil, nil)
	out, err := h(context.Background(), nil)
	assert.NoError(t, err)

	raw, err := json.Marshal(out)
	assert.NoError(t, err)

	var got methods.DaemonStatusResult
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "v0.1.0", got.Version)
	assert.Equal(t, kp.PeerID, got.PeerID)
	assert.Equal(t, startedAt, got.StartedAt)
	assert.NotNil(t, got.ListenAddrs)
	assert.NotNil(t, got.CurrentCalls)
	assert.Equal(t, 0, len(got.ListenAddrs))
	assert.Equal(t, 0, len(got.CurrentCalls))
	assert.Equal(t, "unknown", got.Reachability)
}

func TestDaemonStatus_IncludesListenAddrs(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)
	startedAt := time.Now().UTC()
	addrs := []string{"/ip4/127.0.0.1/tcp/4001/p2p/12D3KooW..."}

	h := methods.DaemonStatus("v1", kp, startedAt, func() []string { return addrs }, func() string { return "public" }, nil)
	out, err := h(context.Background(), nil)
	assert.NoError(t, err)

	raw, _ := json.Marshal(out)
	var got methods.DaemonStatusResult
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, addrs, got.ListenAddrs)
	assert.Equal(t, "public", got.Reachability)
}

func TestDaemonStatus_ReachabilityPropagates(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)
	startedAt := time.Now().UTC()

	h := methods.DaemonStatus("v1", kp, startedAt,
		func() []string { return nil },
		func() string { return "private" },
		nil)
	out, err := h(context.Background(), nil)
	assert.NoError(t, err)
	res, ok := out.(methods.DaemonStatusResult)
	assert.True(t, ok)
	assert.Equal(t, "private", res.Reachability)
}
