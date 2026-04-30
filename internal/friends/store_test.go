package friends_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/friends"
	"opencom/internal/identity"
)

func makeFriend(t *testing.T, name string) friends.Friend {
	t.Helper()
	kp, err := identity.Generate()
	assert.NoError(t, err)
	pub, err := identity.Export(kp, name)
	assert.NoError(t, err)
	return friends.Friend{
		Name:      name,
		PeerID:    kp.PeerID,
		PublicKey: pub.PublicKey,
		AddedAt:   time.Now().UTC(),
	}
}

func TestOpen_CreatesEmptyFileIfMissing(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s, err := friends.Open(path)
	assert.NoError(t, err)
	assert.NotNil(t, s)
	assert.Equal(t, 0, len(s.List()))

	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, "[]\n", string(data))
}

func TestAddListGet_RoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s, err := friends.Open(path)
	assert.NoError(t, err)

	alice := makeFriend(t, "Alice")
	assert.NoError(t, s.Add(alice))

	got, ok := s.Get("Alice")
	assert.True(t, ok)
	assert.Equal(t, alice.PeerID, got.PeerID)

	got2, ok := s.GetByPeerID(alice.PeerID)
	assert.True(t, ok)
	assert.Equal(t, "Alice", got2.Name)

	assert.Equal(t, 1, len(s.List()))
}

func TestAdd_RejectsDuplicateName(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s, err := friends.Open(path)
	assert.NoError(t, err)

	alice := makeFriend(t, "Alice")
	assert.NoError(t, s.Add(alice))

	dup := makeFriend(t, "Alice")
	err = s.Add(dup)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestAdd_RejectsDuplicatePeerID(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s, err := friends.Open(path)
	assert.NoError(t, err)

	alice := makeFriend(t, "Alice")
	assert.NoError(t, s.Add(alice))

	twin := alice
	twin.Name = "Alicia"
	err = s.Add(twin)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "peer id")
}

func TestRemove_RemovesByName(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s, err := friends.Open(path)
	assert.NoError(t, err)
	assert.NoError(t, s.Add(makeFriend(t, "Alice")))
	assert.NoError(t, s.Add(makeFriend(t, "Bob")))

	assert.NoError(t, s.Remove("Alice"))
	_, ok := s.Get("Alice")
	assert.False(t, ok)
	assert.Equal(t, 1, len(s.List()))
}

func TestRemove_ErrorsIfMissing(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s, err := friends.Open(path)
	assert.NoError(t, err)

	err = s.Remove("Nobody")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRename_UpdatesNameKeepsPeerID(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s, err := friends.Open(path)
	assert.NoError(t, err)

	alice := makeFriend(t, "Alice")
	assert.NoError(t, s.Add(alice))

	assert.NoError(t, s.Rename("Alice", "Allie"))
	_, ok := s.Get("Alice")
	assert.False(t, ok)
	got, ok := s.Get("Allie")
	assert.True(t, ok)
	assert.Equal(t, alice.PeerID, got.PeerID)
}

func TestRename_ErrorsIfTargetExists(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s, err := friends.Open(path)
	assert.NoError(t, err)
	assert.NoError(t, s.Add(makeFriend(t, "Alice")))
	assert.NoError(t, s.Add(makeFriend(t, "Bob")))

	err = s.Rename("Alice", "Bob")
	assert.Error(t, err)
}

func TestList_SortedByName(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s, err := friends.Open(path)
	assert.NoError(t, err)
	assert.NoError(t, s.Add(makeFriend(t, "Carol")))
	assert.NoError(t, s.Add(makeFriend(t, "Alice")))
	assert.NoError(t, s.Add(makeFriend(t, "Bob")))

	list := s.List()
	assert.Equal(t, []string{"Alice", "Bob", "Carol"},
		[]string{list[0].Name, list[1].Name, list[2].Name})
}

func TestPeerIDs_ReturnsAllPeers(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s, err := friends.Open(path)
	assert.NoError(t, err)

	alice := makeFriend(t, "Alice")
	bob := makeFriend(t, "Bob")
	assert.NoError(t, s.Add(alice))
	assert.NoError(t, s.Add(bob))

	ids := s.PeerIDs()
	assert.Len(t, ids, 2)
	assert.Contains(t, ids, alice.PeerID)
	assert.Contains(t, ids, bob.PeerID)
}

func TestOpen_ReadsExistingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	alice := makeFriend(t, "Alice")
	body, err := json.MarshalIndent([]friends.Friend{alice}, "", "  ")
	assert.NoError(t, err)
	assert.NoError(t, os.WriteFile(path, body, 0o600))

	s, err := friends.Open(path)
	assert.NoError(t, err)
	got, ok := s.Get("Alice")
	assert.True(t, ok)
	assert.Equal(t, alice.PeerID, got.PeerID)
}

func TestStore_PersistsAcrossReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s1, err := friends.Open(path)
	assert.NoError(t, err)
	alice := makeFriend(t, "Alice")
	assert.NoError(t, s1.Add(alice))

	s2, err := friends.Open(path)
	assert.NoError(t, err)
	got, ok := s2.Get("Alice")
	assert.True(t, ok)
	assert.Equal(t, alice.PeerID, got.PeerID)
}

func TestStore_FileMode0600OnWrite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "friends.json")
	s, err := friends.Open(path)
	assert.NoError(t, err)
	assert.NoError(t, s.Add(makeFriend(t, "Alice")))

	info, err := os.Stat(path)
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
