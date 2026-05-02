// internal/cli/tui/friends_pane.go
package tui

import (
	"fmt"
	"strings"
	"time"

	"opencom/internal/ipc/methods"
)

// friendsPane is the left sidebar component. Holds the friends list
// + current selection index. Stateless w.r.t. styling — renders via
// theme.* on every View call.
type friendsPane struct {
	friends  []methods.FriendsListEntry
	selected int
	filter   string // applied to name (case-insensitive substring)
}

func (p *friendsPane) SetFriends(fs []methods.FriendsListEntry) {
	p.friends = fs
	if p.selected >= len(fs) {
		p.selected = 0
	}
}

func (p *friendsPane) Selected() (methods.FriendsListEntry, bool) {
	visible := p.visible()
	if len(visible) == 0 {
		return methods.FriendsListEntry{}, false
	}
	if p.selected >= len(visible) {
		p.selected = 0
	}
	return visible[p.selected], true
}

func (p *friendsPane) MoveUp() {
	if p.selected > 0 {
		p.selected--
	}
}

func (p *friendsPane) MoveDown() {
	if p.selected < len(p.visible())-1 {
		p.selected++
	}
}

func (p *friendsPane) SetFilter(s string) { p.filter = s; p.selected = 0 }

func (p *friendsPane) visible() []methods.FriendsListEntry {
	if p.filter == "" {
		return p.friends
	}
	q := strings.ToLower(p.filter)
	out := make([]methods.FriendsListEntry, 0, len(p.friends))
	for _, f := range p.friends {
		if strings.Contains(strings.ToLower(f.Name), q) {
			out = append(out, f)
		}
	}
	return out
}

// View renders the sidebar at the given dimensions. filterActive +
// filterValue control whether the filter input row appears at the top.
func (p *friendsPane) View(width, height int, filterActive bool, filterValue string) string {
	var header string
	innerH := height
	if filterActive {
		header = " /" + filterValue + "█\n"
		innerH--
	}

	visible := p.visible()
	if len(visible) == 0 {
		empty := theme.dim.Render("No friends yet")
		return theme.box.Width(width).Height(height).Render(header + empty)
	}
	var b strings.Builder
	b.WriteString(header)
	for i, f := range visible {
		dot := "○"
		if f.Online {
			dot = "●"
		}
		status := "offline"
		if f.Online {
			status = "online"
		} else if !f.LastSeen.IsZero() {
			status = relTime(f.LastSeen)
		}
		// Fallback when the daemon returned a friend with no Name
		// (e.g. invite-redeemed entry where the inviter sent an
		// empty DisplayName). A short peer-ID stub keeps the row
		// readable instead of rendering as a blank line.
		name := f.Name
		if name == "" {
			s := f.PeerID.String()
			if len(s) > 12 {
				name = s[:6] + "…" + s[len(s)-4:]
			} else {
				name = s
			}
		}
		// Render the row in the terminal's default foreground — relying
		// on theme.dim / theme.head colours is fragile against
		// transparent terminals and varied wallpapers (low-contrast
		// glyphs disappear). Selection is conveyed with an ASCII
		// marker so it's visible regardless of palette.
		marker := "  "
		if i == p.selected {
			marker = "▌ "
		}
		nameStr := padOrTrunc(name, 12)
		row := marker + dot + " " + nameStr + " " + status
		b.WriteString(row)
		b.WriteString("\n")
	}
	_ = innerH // height handled by lipgloss .Height(height) on the box
	return theme.box.Width(width).Height(height).Render(b.String())
}

func relTime(t time.Time) string {
	d := time.Since(t).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// padOrTrunc returns s padded with spaces (right) or truncated to
// exactly w visible runes. Operates on runes, not bytes, so multibyte
// glyphs like "…" don't break the alignment.
func padOrTrunc(s string, w int) string {
	rs := []rune(s)
	if len(rs) > w {
		return string(rs[:w-1]) + "…"
	}
	if len(rs) < w {
		return s + strings.Repeat(" ", w-len(rs))
	}
	return s
}
