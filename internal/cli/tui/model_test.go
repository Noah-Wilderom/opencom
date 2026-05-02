package tui_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"opencom/internal/cli/tui"
	"opencom/internal/ipc/methods"
)

func TestModel_InitDispatchesFriendsLoad(t *testing.T) {
	t.Parallel()
	fc := &tui.FakeClient{}
	m := tui.NewModelForTest(fc)
	cmd := m.Init()
	assert.NotNil(t, cmd, "Init should return a Cmd that loads friends")
}

func TestModel_HandlesQuitKey(t *testing.T) {
	t.Parallel()
	m := tui.NewModelForTest(&tui.FakeClient{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	assert.NotNil(t, cmd)
}

// TestModel_SlashEntersFilterMode covers Task 12: pressing `/`
// switches the model into filter mode where typing letters narrows
// the friends list, and esc clears the filter.
func TestModel_SlashEntersFilterMode(t *testing.T) {
	t.Parallel()
	fc := &tui.FakeClient{
		Friends: []methods.FriendsListEntry{
			{Name: "Bob"}, {Name: "Alice"}, {Name: "Bertha"},
		},
	}
	mm := tui.NewModelForTest(fc)
	mm2, _ := mm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	mm = mm2.(tui.Model)
	mm2, _ = mm.Update(tui.FriendsLoadedMsgForTest(fc.Friends))
	mm = mm2.(tui.Model)
	mm2, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm = mm2.(tui.Model)
	// Filter "b" matches Bob and Bertha (case-insensitive substring),
	// excludes Alice.
	mm2, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	mm = mm2.(tui.Model)
	view := mm.View()
	assert.Contains(t, view, "Bob")
	assert.Contains(t, view, "Bertha")
	assert.NotContains(t, view, "Alice")
}

// TestModel_DownArrowMovesFriendsSelection covers Task 11: navigation
// keys move the friends-pane selection and the focus pane re-renders
// to match the newly-selected friend.
func TestModel_DownArrowMovesFriendsSelection(t *testing.T) {
	t.Parallel()
	fc := &tui.FakeClient{
		Friends: []methods.FriendsListEntry{
			{Name: "Bob"},
			{Name: "Alice"},
		},
	}
	m := tui.NewModelForTest(fc)
	m2, _ := m.Update(tui.FriendsLoadedMsgForTest(fc.Friends))
	mm := m2.(tui.Model)
	mm2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm = mm2.(tui.Model)
	sel, ok := tui.SelectedFriendForTest(mm)
	assert.True(t, ok)
	assert.Equal(t, "Alice", sel.Name)
}
