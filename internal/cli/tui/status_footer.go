// internal/cli/tui/status_footer.go
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type statusKind int

const (
	statusIdle statusKind = iota
	statusRinging
	statusConnected
	statusDaemonDown
	statusNoRelay
	statusError
)

// statusFooterState bundles everything the footer renderer needs.
// Held as a value (no pointers) so the parent Model can pass a
// snapshot without sharing mutable state.
type statusFooterState struct {
	Status statusKind
	Detail string // peer name or extra context
	Width  int
}

// renderStatusFooter returns a single-line footer of width Width.
// Left side is the static keybinding hint bar; right side is the
// state indicator (right-aligned via padding).
func renderStatusFooter(s statusFooterState) string {
	keys := strings.Join([]string{
		theme.dim.Render("/") + "search",
		theme.key.Render("a") + " add",
		theme.key.Render("c") + " code",
		theme.key.Render("s") + " settings",
		theme.key.Render("?") + "help",
		theme.key.Render("q") + " quit",
	}, "  ")

	var ind string
	switch s.Status {
	case statusConnected:
		ind = theme.ok.Render("● connected to " + s.Detail)
	case statusRinging:
		ind = theme.warn.Render("● ringing — " + s.Detail)
	case statusDaemonDown:
		ind = theme.err.Render("● daemon down")
	case statusNoRelay:
		ind = theme.warn.Render("⚠ no relay reservation")
	case statusError:
		ind = theme.err.Render("✖ " + s.Detail)
	default:
		ind = theme.dim.Render("● relay reserved")
	}

	if s.Width <= 0 {
		return keys + "  " + ind
	}
	used := lipgloss.Width(keys) + lipgloss.Width(ind)
	if used >= s.Width {
		return keys + "  " + ind
	}
	pad := strings.Repeat(" ", s.Width-used)
	return keys + pad + ind
}
