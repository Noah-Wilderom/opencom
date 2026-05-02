// internal/cli/tui/focus_pane.go
package tui

import (
	"fmt"
	"strings"

	"opencom/internal/ipc/methods"
)

// focusPane is the right column: detail for the currently-selected
// friend. nil friend = empty state with a call-to-action.
type focusPane struct {
	friend *methods.FriendsListEntry
}

func (p *focusPane) SetFriend(f *methods.FriendsListEntry) { p.friend = f }

func (p *focusPane) View(width, height int) string {
	if p.friend == nil {
		body := theme.dim.Render(
			"Press " + theme.key.Render("a") + " to add a friend, or " +
				theme.key.Render("c") + " to generate an invite to share.",
		)
		return theme.box.Width(width).Height(height).Render(body)
	}
	f := p.friend
	status := "offline"
	if f.Online {
		status = "online"
	}
	// Fallback when the daemon returned a friend with no Name. Keep
	// the header non-empty so the layout stays stable.
	name := f.Name
	if name == "" {
		s := f.PeerID.String()
		if len(s) > 12 {
			name = s[:6] + "…" + s[len(s)-4:] + " (no name set)"
		} else {
			name = s + " (no name set)"
		}
	}
	var b strings.Builder
	// Render header + body in default fg — see friends_pane.go for
	// rationale on avoiding theme.head/theme.dim here.
	fmt.Fprintf(&b, "%s\n\n", name)
	fmt.Fprintf(&b, "peer id     %s\n", f.PeerID.String())
	fmt.Fprintf(&b, "status      %s\n", status)
	if !f.AddedAt.IsZero() {
		fmt.Fprintf(&b, "added       %s\n", f.AddedAt.Format("2006-01-02"))
	}
	fmt.Fprintf(&b, "\n%s call   %s rename   %s remove\n",
		theme.key.Render("enter"), theme.key.Render("r"), theme.key.Render("x"))
	return theme.box.Width(width).Height(height).Render(b.String())
}
