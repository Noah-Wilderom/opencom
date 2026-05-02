// internal/cli/tui/modal_quit.go
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// quitModal pops on `q` when a call is active. Lets the user choose
// between detaching (call survives in daemon) and hanging up.
type quitModal struct {
	remote string
	Action string // "" | "detach" | "hangup" | "cancel"
}

func newQuitModal(remote string) *quitModal { return &quitModal{remote: remote} }

func (m *quitModal) Update(msg tea.Msg) (modalView, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "d":
			m.Action = "detach"
			return nil, nil
		case "h":
			m.Action = "hangup"
			return nil, nil
		case "esc":
			m.Action = "cancel"
			return nil, nil
		}
	}
	return m, nil
}

func (m *quitModal) View(width, height int) string {
	_ = width
	_ = height
	body := fmt.Sprintf(
		"You're on a call with %s.\n\n[ %s detach (call continues) ]   [ %s hang up ]   [ %s cancel ]",
		theme.head.Render(m.remote),
		theme.key.Render("d"), theme.key.Render("h"), theme.key.Render("esc"),
	)
	return theme.modalBox.Render(body)
}
