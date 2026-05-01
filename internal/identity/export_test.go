package identity_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/identity"
)

func TestExport_PopulatesAllFields(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)

	id, err := identity.Export(kp, "Alice")
	assert.NoError(t, err)
	assert.Equal(t, 1, id.Version)
	assert.Equal(t, "Alice", id.Name)
	assert.Equal(t, kp.PeerID.String(), id.PeerID)
	assert.NotEqual(t, "", id.PublicKey)
}

func TestWriteRead_RoundTrip(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)

	want, err := identity.Export(kp, "Alice")
	assert.NoError(t, err)

	path := filepath.Join(t.TempDir(), "alice.pub.key")
	assert.NoError(t, identity.WriteExport(path, want))

	got, err := identity.ReadExport(path)
	assert.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestRead_RejectsMismatchedPubKeyAndPeerID(t *testing.T) {
	t.Parallel()

	kpA, err := identity.Generate()
	assert.NoError(t, err)
	kpB, err := identity.Generate()
	assert.NoError(t, err)

	tampered, err := identity.Export(kpA, "Alice")
	assert.NoError(t, err)
	// Splice in a different pubkey while keeping kpA's peer ID — should fail.
	other, err := identity.Export(kpB, "Bob")
	assert.NoError(t, err)
	tampered.PublicKey = other.PublicKey

	path := filepath.Join(t.TempDir(), "tampered.pub.key")
	assert.NoError(t, identity.WriteExport(path, tampered))

	_, err = identity.ReadExport(path)
	assert.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "peer id")
}

func TestPubKey_ReturnsUsableKey(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)

	id, err := identity.Export(kp, "Alice")
	assert.NoError(t, err)

	pub, err := id.PubKey()
	assert.NoError(t, err)
	assert.True(t, pub.Equals(kp.Pub))
}

func TestWriteExport_FileMode0644(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not enforced on Windows")
	}
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)

	id, err := identity.Export(kp, "Alice")
	assert.NoError(t, err)

	path := filepath.Join(t.TempDir(), "alice.pub.key")
	assert.NoError(t, identity.WriteExport(path, id))

	info, err := os.Stat(path)
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestWriteExport_CreatesNestedParentDirs(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)

	id, err := identity.Export(kp, "Alice")
	assert.NoError(t, err)

	path := filepath.Join(t.TempDir(), "nested", "deep", "alice.pub.key")
	assert.NoError(t, identity.WriteExport(path, id))

	_, err = os.Stat(path)
	assert.NoError(t, err)
}

func TestRead_RejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "v2.pub.key")
	yaml := []byte("version: 2\nname: Alice\npeer_id: x\npublic_key: y\n")
	assert.NoError(t, os.WriteFile(path, yaml, 0o644))

	_, err := identity.ReadExport(path)
	assert.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "unsupported")
}

func TestPubKey_RejectsTamperedBase64(t *testing.T) {
	t.Parallel()

	id := identity.PublicIdentity{
		Version:   1,
		Name:      "Alice",
		PeerID:    "anything",
		PublicKey: "!!!not-base64!!!",
	}
	_, err := id.PubKey()
	assert.Error(t, err)
}
