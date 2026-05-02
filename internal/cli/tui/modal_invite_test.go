// internal/cli/tui/modal_invite_test.go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"opencom/internal/ipc/methods"
)

func TestInviteModal_LoadingStateShowsSpinner(t *testing.T) {
	t.Parallel()
	m := newInviteModal(&FakeClipboard{})
	out := m.View(80, 24)
	assert.Contains(t, out, "contacting daemon")
}

func TestInviteModal_ActiveStateShowsCodeAndURL(t *testing.T) {
	t.Parallel()
	m := newInviteModal(&FakeClipboard{})
	m.SetResult(methods.InviteCreateResult{
		Code: "OPEN-A7B2-X9K4",
		URL:  "opencom://join?p=12D3KooW…",
	})
	out := m.View(80, 24)
	assert.Contains(t, out, "OPEN-A7B2-X9K4")
	assert.Contains(t, out, "opencom://")
	// QR characters should appear since URL was rendered.
	assert.True(t, len(out) > 200, "active state should be substantial")
}

func TestInviteModal_CopyURLWritesClipboard(t *testing.T) {
	t.Parallel()
	cb := &FakeClipboard{}
	m := newInviteModal(cb)
	m.SetResult(methods.InviteCreateResult{
		Code: "OPEN-A7B2-X9K4",
		URL:  "opencom://join?p=test",
	})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	assert.Equal(t, "opencom://join?p=test", cb.Contents)
}

func TestInviteModal_CopyCodeWritesClipboard(t *testing.T) {
	t.Parallel()
	cb := &FakeClipboard{}
	m := newInviteModal(cb)
	m.SetResult(methods.InviteCreateResult{
		Code: "OPEN-A7B2-X9K4",
		URL:  "opencom://join?p=test",
	})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	assert.Equal(t, "OPEN-A7B2-X9K4", cb.Contents)
}

func TestInviteModal_RedeemedStateShowsToast(t *testing.T) {
	t.Parallel()
	m := newInviteModal(&FakeClipboard{})
	m.SetResult(methods.InviteCreateResult{Code: "OPEN-A7B2-X9K4"})
	m.SetRedeemed("Alice")
	out := m.View(80, 24)
	assert.Contains(t, out, "redeemed by")
	assert.Contains(t, out, "Alice")
}

func TestInviteModal_EscClosesModal(t *testing.T) {
	t.Parallel()
	m := newInviteModal(&FakeClipboard{})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Nil(t, next)
}
