// internal/cli/tui/modal_invite.go
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"opencom/internal/ipc/methods"
)

// inviteModalState walks through three internal states:
//
//	inviteStateLoading   — invite.create in flight
//	inviteStateActive    — code/URL/QR shown, waiting for keypress or redemption
//	inviteStateRedeemed  — confirmation toast (TODO: auto-close after 2s
//	                       via tea.Tick; for now the user dismisses with esc)
type inviteModalState int

const (
	inviteStateLoading inviteModalState = iota
	inviteStateActive
	inviteStateRedeemed
)

// inviteModal implements modalView. Action records what the parent
// model should do after the modal closes:
//
//	""        — nothing (modal closed normally)
type inviteModal struct {
	state    inviteModalState
	result   methods.InviteCreateResult
	qr       string // pre-rendered ASCII QR of result.URL; empty until state becomes active
	clip     Clipboard
	redeemer string // peer ID string of the redeemer, populated in stateRedeemed
	Action   string
}

// newInviteModal returns the initial loading-state modal. The parent
// Model dispatches createInviteCmd alongside opening the modal; when
// inviteCreatedMsg arrives, it calls SetResult to advance the state.
func newInviteModal(clip Clipboard) *inviteModal {
	return &inviteModal{state: inviteStateLoading, clip: clip}
}

// SetResult transitions from loading to active. Called by the parent
// Model when it receives inviteCreatedMsg.
func (m *inviteModal) SetResult(r methods.InviteCreateResult) {
	m.result = r
	if q, err := renderASCIIQR(r.URL); err == nil {
		m.qr = q
	}
	m.state = inviteStateActive
}

// SetRedeemed transitions to the brief "redeemed by X" toast.
//
// TODO: schedule a tea.Tick (2s) to auto-close the modal once the
// toast has been shown. v1 leaves the modal open until the user
// presses esc.
func (m *inviteModal) SetRedeemed(by string) {
	m.redeemer = by
	m.state = inviteStateRedeemed
}

// Code returns the invite's pretty code (for the parent Model to
// pass to subscribeInviteRedemptionCmd).
func (m *inviteModal) Code() string { return m.result.Code }

// Update handles key events. Returns nil to close the modal.
func (m *inviteModal) Update(msg tea.Msg) (modalView, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return nil, nil
		case "u":
			if m.state == inviteStateActive && m.clip != nil {
				_ = m.clip.Write(m.result.URL)
			}
		case "k":
			if m.state == inviteStateActive && m.clip != nil {
				_ = m.clip.Write(m.result.Code)
			}
		}
	}
	return m, nil
}

// View returns the modal panel only — the parent Model composes the
// overlay via renderOverlay.
func (m *inviteModal) View(width, height int) string {
	_ = width
	_ = height
	var b strings.Builder
	b.WriteString(theme.head.Render("Generate invite"))
	b.WriteString("\n\n")
	switch m.state {
	case inviteStateLoading:
		b.WriteString(theme.dim.Render("contacting daemon…"))
	case inviteStateActive:
		fmt.Fprintf(&b, "code: %s\n", theme.head.Render(m.result.Code))
		fmt.Fprintf(&b, "url:  %s\n\n", m.result.URL)
		if m.qr != "" {
			b.WriteString(m.qr)
			b.WriteString("\n")
		}
		if m.result.DHTPublishWarning != "" {
			fmt.Fprintf(&b, "%s %s\n", theme.warn.Render("⚠"), m.result.DHTPublishWarning)
		}
		fmt.Fprintf(&b, "\n[ %s copy URL ]   [ %s copy code ]   [ %s close ]",
			theme.key.Render("u"), theme.key.Render("k"), theme.key.Render("esc"))
	case inviteStateRedeemed:
		fmt.Fprintf(&b, "%s redeemed by %s", theme.ok.Render("✓"), theme.head.Render(m.redeemer))
	}
	return theme.modalBox.Render(b.String())
}
