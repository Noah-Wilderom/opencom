package methods_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/invite"
	"opencom/internal/ipc/methods"
)

func TestInviteList_ReturnsActive(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "store.json")
	s, err := invite.OpenStore(path)
	assert.NoError(t, err)

	now := time.Now()
	s.Add(invite.Entry{Code: "ACTIVE01", ExpiresAt: now.Add(30 * time.Minute), CreatedAt: now})
	s.Add(invite.Entry{Code: "CONSUMED", ExpiresAt: now.Add(30 * time.Minute), CreatedAt: now})
	s.MarkConsumed("CONSUMED", "12D3KooWPeer")

	h := methods.InviteList(s)
	out, err := h(context.Background(), nil)
	assert.NoError(t, err)
	raw, _ := json.Marshal(out)
	var got methods.InviteListResult
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Len(t, got.Invites, 2)

	// Find ACTIVE01 and CONSUMED entries; check Active flag.
	var foundActive, foundConsumed bool
	for _, e := range got.Invites {
		switch e.Code {
		case "ACTIVE01":
			assert.True(t, e.Active)
			assert.False(t, e.Consumed)
			foundActive = true
		case "CONSUMED":
			assert.False(t, e.Active)
			assert.True(t, e.Consumed)
			assert.Equal(t, "12D3KooWPeer", e.ConsumedBy)
			foundConsumed = true
		}
	}
	assert.True(t, foundActive)
	assert.True(t, foundConsumed)
}

func TestInviteList_PrettyFormat(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "store.json")
	s, err := invite.OpenStore(path)
	assert.NoError(t, err)
	s.Add(invite.Entry{Code: invite.Code("A7B2X9K4"), ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()})

	h := methods.InviteList(s)
	out, err := h(context.Background(), nil)
	assert.NoError(t, err)
	raw, _ := json.Marshal(out)
	var got methods.InviteListResult
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Len(t, got.Invites, 1)
	assert.Equal(t, "OPEN-A7B2-X9K4", got.Invites[0].Pretty)
}
