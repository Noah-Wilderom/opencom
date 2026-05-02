// internal/cli/tui/modal_add_friend_test.go
package tui

import (
	"encoding/base64"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"

	"opencom/internal/ipc/methods"
)

func TestAddFriendModal_EmptyClipboardShowsManualForm(t *testing.T) {
	t.Parallel()
	cb := &FakeClipboard{Contents: ""}
	m := newAddFriendModal(cb, &FakeClient{})
	out := m.View(80, 24)
	assert.Contains(t, out, "Paste")
	assert.Contains(t, out, "no invite found")
}

func TestAddFriendModal_NewPeerOffersAdd(t *testing.T) {
	t.Parallel()
	cb := &FakeClipboard{Contents: "OPEN-A7B2-X9K4"}
	m := newAddFriendModal(cb, &FakeClient{Friends: nil})
	out := m.View(80, 24)
	assert.True(t, strings.Contains(out, "found invite") || strings.Contains(out, "OPEN-"),
		"new-peer state should acknowledge the detected invite")
}

func TestAddFriendModal_DuplicateBlocksWhenURLInvite(t *testing.T) {
	t.Parallel()
	// peer.ID is a stringified raw-bytes type; both sides of the
	// duplicate check call .String() (base58) so we use the same
	// peer.ID value on both sides.
	pid := peer.ID("12D3KooWAlreadyAFriendXxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	// invite.ParseURL requires p (peer id), a (addresses, non-empty
	// after base64-rawurl decode) and c (a valid 8-char Crockford
	// base-32 code). We skip k/e/s — the modal only relies on
	// ParseURL succeeding + the embedded peer id matching a friend.
	addrEnc := base64.RawURLEncoding.EncodeToString([]byte("/ip4/127.0.0.1/tcp/1"))
	url := "opencom://join?p=" + pid.String() + "&a=" + addrEnc + "&n=Alice&c=A7B2X9K4"
	cb := &FakeClipboard{Contents: url}
	m := newAddFriendModal(cb, &FakeClient{
		Friends: []methods.FriendsListEntry{{Name: "Alice", PeerID: pid}},
	})
	out := m.View(80, 24)
	assert.Contains(t, out, "already")
}

func TestAddFriendModal_EnterDispatchesRedeemAction(t *testing.T) {
	t.Parallel()
	cb := &FakeClipboard{Contents: "OPEN-A7B2-X9K4"}
	m := newAddFriendModal(cb, &FakeClient{})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Nil(t, next, "modal should close on enter when invite is detected")
	assert.Equal(t, "redeem", m.Action)
}
