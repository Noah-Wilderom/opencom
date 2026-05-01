package cli_test

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/cli"
	"opencom/internal/friends"
	"opencom/internal/identity"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
)

func startFriendsServer(t *testing.T, store *friends.Store, presence *friends.Presence) func() {
	t.Helper()
	skipIfWindowsNoUnixSockets(t)
	root := os.Getenv("XDG_RUNTIME_DIR")
	assert.NoError(t, os.MkdirAll(root, 0o700))
	sock := filepath.Join(root, "opencom.sock")

	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)
	assert.NoError(t, os.Chmod(sock, 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	s := ipc.NewServer(zap.NewNop(), "test")
	s.Register("friends.add", methods.FriendsAdd(store))
	s.Register("friends.list", methods.FriendsList(store, presence))
	s.Register("friends.remove", methods.FriendsRemove(store))
	s.Register("friends.rename", methods.FriendsRename(store))
	s.Register("friends.show", methods.FriendsShow(store, presence))

	go func() { _ = s.Serve(ctx, ln) }()
	return func() {
		cancel()
		_ = ln.Close()
		_ = os.Remove(sock)
	}
}

func writeKeyFile(t *testing.T, dir, name string) string {
	t.Helper()
	kp, err := identity.Generate()
	assert.NoError(t, err)
	pub, err := identity.Export(kp, name)
	assert.NoError(t, err)
	path := filepath.Join(dir, name+".pub.key")
	assert.NoError(t, identity.WriteExport(path, pub))
	return path
}

func TestFriendsAdd_PrintsAddedConfirmation(t *testing.T) {
	withTempPaths(t)

	store, err := friends.Open(filepath.Join(t.TempDir(), "friends.json"))
	assert.NoError(t, err)
	pres := friends.NewPresence(time.Now)
	stop := startFriendsServer(t, store, pres)
	defer stop()

	keyPath := writeKeyFile(t, t.TempDir(), "Alice")

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"friends", "add", keyPath})
	assert.NoError(t, root.Execute())

	assert.Contains(t, out.String(), "Added Alice")
}

func TestFriendsList_PrintsEmptyMessageWhenNoFriends(t *testing.T) {
	withTempPaths(t)

	store, err := friends.Open(filepath.Join(t.TempDir(), "friends.json"))
	assert.NoError(t, err)
	pres := friends.NewPresence(time.Now)
	stop := startFriendsServer(t, store, pres)
	defer stop()

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"friends", "list"})
	assert.NoError(t, root.Execute())

	assert.Contains(t, out.String(), "no friends yet")
}

func TestFriendsList_TabularOutput(t *testing.T) {
	withTempPaths(t)

	storeDir := t.TempDir()
	store, err := friends.Open(filepath.Join(storeDir, "friends.json"))
	assert.NoError(t, err)
	pres := friends.NewPresence(time.Now)
	stop := startFriendsServer(t, store, pres)
	defer stop()

	keyPath := writeKeyFile(t, storeDir, "Alice")
	root1 := cli.NewRootCmd()
	root1.SetOut(&bytes.Buffer{})
	root1.SetErr(&bytes.Buffer{})
	root1.SetArgs([]string{"friends", "add", keyPath})
	assert.NoError(t, root1.Execute())

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"friends", "list"})
	assert.NoError(t, root.Execute())

	body := out.String()
	assert.Contains(t, body, "Alice")
	assert.Contains(t, body, "offline")
}

func TestFriendsRemove_RemovesAndPrints(t *testing.T) {
	withTempPaths(t)

	storeDir := t.TempDir()
	store, err := friends.Open(filepath.Join(storeDir, "friends.json"))
	assert.NoError(t, err)
	pres := friends.NewPresence(time.Now)
	stop := startFriendsServer(t, store, pres)
	defer stop()

	keyPath := writeKeyFile(t, storeDir, "Alice")
	root1 := cli.NewRootCmd()
	root1.SetOut(&bytes.Buffer{})
	root1.SetErr(&bytes.Buffer{})
	root1.SetArgs([]string{"friends", "add", keyPath})
	assert.NoError(t, root1.Execute())

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"friends", "remove", "Alice"})
	assert.NoError(t, root.Execute())
	assert.Contains(t, out.String(), "Removed Alice")
}

func TestFriendsRename_RenamesAndPrints(t *testing.T) {
	withTempPaths(t)

	storeDir := t.TempDir()
	store, err := friends.Open(filepath.Join(storeDir, "friends.json"))
	assert.NoError(t, err)
	pres := friends.NewPresence(time.Now)
	stop := startFriendsServer(t, store, pres)
	defer stop()

	keyPath := writeKeyFile(t, storeDir, "Alice")
	root1 := cli.NewRootCmd()
	root1.SetOut(&bytes.Buffer{})
	root1.SetErr(&bytes.Buffer{})
	root1.SetArgs([]string{"friends", "add", keyPath})
	assert.NoError(t, root1.Execute())

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"friends", "rename", "Alice", "--to=Allie"})
	assert.NoError(t, root.Execute())
	assert.Contains(t, out.String(), "Renamed Alice -> Allie")
}

func TestFriendsShow_PrintsDetail(t *testing.T) {
	withTempPaths(t)

	storeDir := t.TempDir()
	store, err := friends.Open(filepath.Join(storeDir, "friends.json"))
	assert.NoError(t, err)
	pres := friends.NewPresence(time.Now)
	stop := startFriendsServer(t, store, pres)
	defer stop()

	keyPath := writeKeyFile(t, storeDir, "Alice")
	root1 := cli.NewRootCmd()
	root1.SetOut(&bytes.Buffer{})
	root1.SetErr(&bytes.Buffer{})
	root1.SetArgs([]string{"friends", "add", keyPath})
	assert.NoError(t, root1.Execute())

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"friends", "show", "Alice"})
	assert.NoError(t, root.Execute())
	body := out.String()
	assert.Contains(t, body, "Name        : Alice")
	assert.Contains(t, body, "Peer ID     :")
}
