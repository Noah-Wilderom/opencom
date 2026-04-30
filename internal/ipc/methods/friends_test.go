package methods_test

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/friends"
	"opencom/internal/identity"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
)

func writeFriendKey(t *testing.T, dir, name string) (string, identity.Keypair) {
	t.Helper()
	kp, err := identity.Generate()
	assert.NoError(t, err)
	pub, err := identity.Export(kp, name)
	assert.NoError(t, err)
	path := filepath.Join(dir, name+".pub.key")
	assert.NoError(t, identity.WriteExport(path, pub))
	return path, kp
}

func TestFriendsAdd_AddsToStore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := friends.Open(filepath.Join(dir, "friends.json"))
	assert.NoError(t, err)

	kpPath, kp := writeFriendKey(t, dir, "Alice")
	h := methods.FriendsAdd(store)

	params, _ := json.Marshal(methods.FriendsAddParams{KeyPath: kpPath})
	out, err := h(context.Background(), params)
	assert.NoError(t, err)

	raw, _ := json.Marshal(out)
	var got methods.FriendsListEntry
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "Alice", got.Name)
	assert.Equal(t, kp.PeerID, got.PeerID)

	stored, ok := store.Get("Alice")
	assert.True(t, ok)
	assert.Equal(t, kp.PeerID, stored.PeerID)
}

func TestFriendsAdd_NameOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := friends.Open(filepath.Join(dir, "friends.json"))
	assert.NoError(t, err)

	kpPath, _ := writeFriendKey(t, dir, "Alice")
	h := methods.FriendsAdd(store)
	params, _ := json.Marshal(methods.FriendsAddParams{KeyPath: kpPath, Name: "Allie"})
	_, err = h(context.Background(), params)
	assert.NoError(t, err)

	_, ok := store.Get("Allie")
	assert.True(t, ok)
}

func TestFriendsAdd_RejectsMissingKeyPath(t *testing.T) {
	t.Parallel()

	store, err := friends.Open(filepath.Join(t.TempDir(), "friends.json"))
	assert.NoError(t, err)
	h := methods.FriendsAdd(store)

	params, _ := json.Marshal(methods.FriendsAddParams{})
	_, err = h(context.Background(), params)
	assert.Error(t, err)
	var rpcErr *ipc.Error
	assert.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, ipc.ErrCodeInvalidParams, rpcErr.Code)
}

func TestFriendsList_IncludesOnlineState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := friends.Open(filepath.Join(dir, "friends.json"))
	assert.NoError(t, err)
	pres := friends.NewPresence(time.Now)

	kpPath, kp := writeFriendKey(t, dir, "Alice")
	add := methods.FriendsAdd(store)
	addParams, _ := json.Marshal(methods.FriendsAddParams{KeyPath: kpPath})
	_, err = add(context.Background(), addParams)
	assert.NoError(t, err)

	pres.MarkOnline(kp.PeerID)

	h := methods.FriendsList(store, pres)
	out, err := h(context.Background(), nil)
	assert.NoError(t, err)
	raw, _ := json.Marshal(out)
	var got methods.FriendsListResult
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Len(t, got.Friends, 1)
	assert.Equal(t, "Alice", got.Friends[0].Name)
	assert.True(t, got.Friends[0].Online)
}

func TestFriendsRemove_RemovesByName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := friends.Open(filepath.Join(dir, "friends.json"))
	assert.NoError(t, err)
	kpPath, _ := writeFriendKey(t, dir, "Alice")
	add := methods.FriendsAdd(store)
	addParams, _ := json.Marshal(methods.FriendsAddParams{KeyPath: kpPath})
	_, err = add(context.Background(), addParams)
	assert.NoError(t, err)

	h := methods.FriendsRemove(store)
	params, _ := json.Marshal(methods.FriendsRemoveParams{Name: "Alice"})
	_, err = h(context.Background(), params)
	assert.NoError(t, err)

	_, ok := store.Get("Alice")
	assert.False(t, ok)
}

func TestFriendsRemove_NoSuchFriend(t *testing.T) {
	t.Parallel()

	store, err := friends.Open(filepath.Join(t.TempDir(), "friends.json"))
	assert.NoError(t, err)
	h := methods.FriendsRemove(store)
	params, _ := json.Marshal(methods.FriendsRemoveParams{Name: "Nobody"})
	_, err = h(context.Background(), params)
	assert.Error(t, err)
	var rpcErr *ipc.Error
	assert.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, ipc.ErrCodeNoSuchFriend, rpcErr.Code)
}

func TestFriendsRename_RenamesByName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := friends.Open(filepath.Join(dir, "friends.json"))
	assert.NoError(t, err)
	kpPath, _ := writeFriendKey(t, dir, "Alice")
	add := methods.FriendsAdd(store)
	addParams, _ := json.Marshal(methods.FriendsAddParams{KeyPath: kpPath})
	_, err = add(context.Background(), addParams)
	assert.NoError(t, err)

	h := methods.FriendsRename(store)
	params, _ := json.Marshal(methods.FriendsRenameParams{Name: "Alice", NewName: "Allie"})
	_, err = h(context.Background(), params)
	assert.NoError(t, err)

	_, ok := store.Get("Allie")
	assert.True(t, ok)
}

func TestFriendsShow_ReturnsFullRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := friends.Open(filepath.Join(dir, "friends.json"))
	assert.NoError(t, err)
	pres := friends.NewPresence(time.Now)
	kpPath, kp := writeFriendKey(t, dir, "Alice")
	add := methods.FriendsAdd(store)
	addParams, _ := json.Marshal(methods.FriendsAddParams{KeyPath: kpPath})
	_, err = add(context.Background(), addParams)
	assert.NoError(t, err)

	h := methods.FriendsShow(store, pres)
	params, _ := json.Marshal(methods.FriendsShowParams{Name: "Alice"})
	out, err := h(context.Background(), params)
	assert.NoError(t, err)
	raw, _ := json.Marshal(out)
	var got methods.FriendsShowResult
	assert.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "Alice", got.Name)
	assert.Equal(t, kp.PeerID, got.PeerID)
	assert.NotEmpty(t, got.PublicKey)
}

func TestFriendsSubscribePresence_DeliversEvents(t *testing.T) {
	t.Parallel()

	pres := friends.NewPresence(time.Now)
	h := methods.FriendsSubscribePresence(pres)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock := startMethodsServer(t, ctx, func(s *ipc.Server) {
		s.Register("friends.subscribe_presence", h)
	})

	c, err := ipc.Dial(context.Background(), sock)
	assert.NoError(t, err)
	defer c.Close()

	sub, err := c.Subscribe(context.Background(), "friends.subscribe_presence", nil)
	assert.NoError(t, err)
	defer sub.Close()

	kp, err := identity.Generate()
	assert.NoError(t, err)
	pres.MarkOnline(kp.PeerID)

	select {
	case ev := <-sub.Events:
		assert.Equal(t, "presence_changed", ev.Kind)
		var pe friends.PresenceEvent
		assert.NoError(t, json.Unmarshal(ev.Data, &pe))
		assert.Equal(t, kp.PeerID, pe.PeerID)
		assert.True(t, pe.Online)
	case <-time.After(2 * time.Second):
		t.Fatal("no event received")
	}
}

// startMethodsServer launches an in-process IPC server listening on a
// Unix socket inside t.TempDir(); the server is shut down when ctx is
// canceled.
func startMethodsServer(t *testing.T, ctx context.Context, register func(s *ipc.Server)) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "test.sock")
	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)

	s := ipc.NewServer(zap.NewNop(), "test")
	if register != nil {
		register(s)
	}
	go func() { _ = s.Serve(ctx, ln) }()
	return sock
}
