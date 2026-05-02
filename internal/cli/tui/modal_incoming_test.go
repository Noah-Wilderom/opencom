// internal/cli/tui/modal_incoming_test.go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestIncomingModal_RendersCallerAndKeys(t *testing.T) {
	t.Parallel()
	im := newIncomingModal(incomingModalState{
		Caller:      "Alice",
		PeerID:      "12D3KooW…d3ab",
		Fingerprint: "sha256:9c1a…e88f",
		Verified:    false,
	})
	view := im.View(80, 24)
	assert.Contains(t, view, "Incoming call")
	assert.Contains(t, view, "Alice")
	assert.Contains(t, view, "accept")
	assert.Contains(t, view, "decline")
	assert.Contains(t, view, "unverified")
}

func TestIncomingModal_AcceptKey(t *testing.T) {
	t.Parallel()
	im := newIncomingModal(incomingModalState{Caller: "Alice"})
	next, _ := im.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	assert.Nil(t, next, "modal should close on accept")
	assert.Equal(t, "accept", im.Action)
}

func TestIncomingModal_DeclineKey(t *testing.T) {
	t.Parallel()
	im := newIncomingModal(incomingModalState{Caller: "Alice"})
	_, _ = im.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	assert.Equal(t, "decline", im.Action)
}
