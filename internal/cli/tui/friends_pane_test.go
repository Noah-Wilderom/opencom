// internal/cli/tui/friends_pane_test.go
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/ipc/methods"
)

func TestFriendsPane_RendersOnlineDot(t *testing.T) {
	t.Parallel()
	p := friendsPane{}
	p.SetFriends([]methods.FriendsListEntry{
		{Name: "Bob", Online: true},
		{Name: "Alice", Online: false, LastSeen: time.Now().Add(-5 * time.Minute)},
	})
	out := p.View(28, 20, false, "")
	assert.Contains(t, out, "Bob")
	assert.Contains(t, out, "Alice")
	assert.True(t, strings.Contains(out, "●") || strings.Contains(out, "○"),
		"presence indicator should appear")
}

func TestFriendsPane_EmptyState(t *testing.T) {
	t.Parallel()
	p := friendsPane{}
	out := p.View(28, 20, false, "")
	assert.Contains(t, out, "No friends yet")
}

func TestFriendsPane_FilterShowsRow(t *testing.T) {
	t.Parallel()
	p := friendsPane{}
	out := p.View(28, 20, true, "ali")
	assert.Contains(t, out, "/ali")
}
