// internal/cli/tui/focus_pane_test.go
package tui

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"

	"opencom/internal/ipc/methods"
)

func TestFocusPane_ShowsSelectedFriend(t *testing.T) {
	t.Parallel()
	p := focusPane{}
	p.SetFriend(&methods.FriendsListEntry{
		Name:    "Bob",
		PeerID:  peer.ID("12D3KooWBobXxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"),
		Online:  true,
		AddedAt: time.Now().AddDate(0, -1, 0),
	})
	out := p.View(60, 20)
	assert.Contains(t, out, "Bob")
	assert.Contains(t, out, "peer id")
	assert.Contains(t, out, "online")
}

func TestFocusPane_EmptyStateInvitesAdd(t *testing.T) {
	t.Parallel()
	p := focusPane{}
	out := p.View(60, 20)
	assert.Contains(t, out, "Press")
	assert.Contains(t, out, "add a friend")
}
