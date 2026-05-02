// internal/cli/tui/modal_help.go
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// helpModal is the centred overlay listing all keybindings, grouped
// by context. Modeled on lazygit / k9s.
type helpModal struct{}

func newHelpModal() *helpModal { return &helpModal{} }

func (m *helpModal) Update(msg tea.Msg) (modalView, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "?", "esc":
			return nil, nil
		}
	}
	return m, nil
}

func (m *helpModal) View(width, height int) string {
	groups := []struct {
		title string
		items [][2]string
	}{
		{"Global", [][2]string{
			{"/", "filter friends"},
			{"a", "add friend"},
			{"c", "generate invite"},
			{"s", "settings"},
			{"?", "this help"},
			{"q", "quit"},
		}},
		{"Friends", [][2]string{
			{"↑/k", "select previous"},
			{"↓/j", "select next"},
			{"enter", "place call"},
			{"r", "rename"},
			{"x", "remove"},
		}},
		{"In call", [][2]string{
			{"m", "toggle mute"},
			{"h", "hang up"},
			{"esc", "detach (call lives in daemon)"},
		}},
		{"In modal", [][2]string{
			{"esc", "cancel / close"},
			{"enter", "confirm"},
		}},
	}
	var b strings.Builder
	b.WriteString(theme.head.Render("Keybindings"))
	b.WriteString("\n\n")
	for _, g := range groups {
		b.WriteString(theme.accent.Render(g.title))
		b.WriteString("\n")
		for _, kv := range g.items {
			b.WriteString("  ")
			b.WriteString(theme.key.Render(padLeft(kv[0], 6)))
			b.WriteString("  ")
			b.WriteString(kv[1])
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(theme.dim.Render("press ? or esc to close"))
	_ = width
	_ = height
	return theme.modalBox.Render(b.String())
}

func padLeft(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return strings.Repeat(" ", w-len(s)) + s
}
