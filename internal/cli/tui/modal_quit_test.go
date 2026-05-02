// internal/cli/tui/modal_quit_test.go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestQuitModal_DetachKeyExitsWithoutHangup(t *testing.T) {
	t.Parallel()
	m := newQuitModal("Bob")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	assert.Equal(t, "detach", m.Action)
}

func TestQuitModal_HangupKey(t *testing.T) {
	t.Parallel()
	m := newQuitModal("Bob")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	assert.Equal(t, "hangup", m.Action)
}

func TestQuitModal_EscCancels(t *testing.T) {
	t.Parallel()
	m := newQuitModal("Bob")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, "cancel", m.Action)
}
