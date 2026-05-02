// internal/cli/tui/modal_help_test.go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestHelpModal_ListsAllKeyGroups(t *testing.T) {
	t.Parallel()
	m := newHelpModal()
	out := m.View(80, 24)
	assert.Contains(t, out, "Global")
	assert.Contains(t, out, "Friends")
	assert.Contains(t, out, "In call")
	assert.Contains(t, out, "/")
	assert.Contains(t, out, "?")
	assert.Contains(t, out, "q")
}

func TestHelpModal_EscClosesModal(t *testing.T) {
	t.Parallel()
	m := newHelpModal()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Nil(t, next)
}
