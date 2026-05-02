// internal/cli/tui/modal_settings_test.go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestSettingsModal_PointsAtConfigPath(t *testing.T) {
	t.Parallel()
	m := newSettingsModal("/home/u/.config/opencom/config.yaml", "")
	out := m.View(80, 24)
	assert.Contains(t, out, "config.yaml")
	assert.Contains(t, out, "$EDITOR")
}

func TestSettingsModal_OpensInjectedEditor(t *testing.T) {
	t.Parallel()
	called := ""
	m := newSettingsModal("/tmp/cfg.yaml", "true")
	m.exec = func(name string, args ...string) error { called = name; return nil }
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	assert.Nil(t, next)
	assert.Equal(t, "true", called)
}
