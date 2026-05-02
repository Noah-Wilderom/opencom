// internal/cli/tui/export_test.go
package tui

import "opencom/internal/ipc/methods"

// FriendsLoadedMsgForTest constructs the unexported friendsLoadedMsg
// so tests in package tui_test can drive Update without booting the
// real loadFriendsCmd / IPC path.
func FriendsLoadedMsgForTest(fs []methods.FriendsListEntry) interface{} {
	return friendsLoadedMsg{Friends: fs}
}

// SelectedFriendForTest exposes the focus-pane state for assertions.
func SelectedFriendForTest(m Model) (methods.FriendsListEntry, bool) {
	if m.focus.friend == nil {
		return methods.FriendsListEntry{}, false
	}
	return *m.focus.friend, true
}
