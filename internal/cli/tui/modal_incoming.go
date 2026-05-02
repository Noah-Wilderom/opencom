// internal/cli/tui/modal_incoming.go
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// incomingModalState bundles the data the modal renders.
type incomingModalState struct {
	CallID      string
	Caller      string
	PeerID      string
	Fingerprint string
	Verified    bool
}

// incomingModal is the centered overlay that pops on inbound ringing.
// Implements modalView. Callers inspect Action after Update returns
// nil to dispatch the chosen IPC action.
type incomingModal struct {
	state  incomingModalState
	Action string // "" | "accept" | "decline" | "ignore"
}

func newIncomingModal(s incomingModalState) *incomingModal { return &incomingModal{state: s} }

func (m *incomingModal) Update(msg tea.Msg) (modalView, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "a":
			m.Action = "accept"
			return nil, nil
		case "d":
			m.Action = "decline"
			return nil, nil
		case "esc":
			m.Action = "ignore"
			return nil, nil
		}
	}
	return m, nil
}

func (m *incomingModal) View(width, height int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", theme.head.Render("Incoming call"))
	fmt.Fprintf(&b, "%s\n", theme.head.Render(m.state.Caller))
	fmt.Fprintf(&b, "%s\n", theme.dim.Render(m.state.PeerID))
	verified := theme.dim.Render("(unverified)")
	if m.state.Verified {
		verified = theme.ok.Render("(verified)")
	}
	fmt.Fprintf(&b, "fingerprint  %s  %s\n\n", m.state.Fingerprint, verified)
	fmt.Fprintf(&b, "[ %s accept ]   [ %s decline ]   [ %s ignore ]",
		theme.key.Render("a"), theme.key.Render("d"), theme.key.Render("esc"))
	_ = width
	_ = height
	return theme.modalBox.Render(b.String())
}
