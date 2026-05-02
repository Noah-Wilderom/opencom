package tui_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/cli/tui"
	"opencom/internal/ipc/methods"
)

func TestClientSurface_FakeImplementsAllMethods(t *testing.T) {
	t.Parallel()
	var _ tui.Client = (*tui.FakeClient)(nil)
}

func TestFakeClient_FriendsListReturnsCannedRows(t *testing.T) {
	t.Parallel()
	fc := &tui.FakeClient{
		Friends: []methods.FriendsListEntry{{Name: "Bob", Online: true}},
	}
	got, err := fc.FriendsList(context.Background())
	assert.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "Bob", got[0].Name)
}
