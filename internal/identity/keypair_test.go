package identity_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/stretchr/testify/assert"

	"opencom/internal/identity"
)

func TestGenerate_ProducesEd25519Keypair(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)
	assert.NotNil(t, kp.Priv)
	assert.NotNil(t, kp.Pub)
	assert.NotEqual(t, "", kp.PeerID.String())
	// EqualValues: crypto.Ed25519 is an untyped const; Type() returns pb.KeyType.
	assert.EqualValues(t, crypto.Ed25519, kp.Priv.Type())
}

func TestGenerate_PeerIDDerivesFromPubkey(t *testing.T) {
	t.Parallel()

	kp, err := identity.Generate()
	assert.NoError(t, err)

	derived, err := identity.PeerIDFromPubKey(kp.Pub)
	assert.NoError(t, err)
	assert.Equal(t, kp.PeerID, derived)
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "priv.key")

	original, err := identity.Generate()
	assert.NoError(t, err)
	assert.NoError(t, identity.Save(path, original))

	loaded, err := identity.Load(path)
	assert.NoError(t, err)
	assert.Equal(t, original.PeerID, loaded.PeerID)
	assert.True(t, original.Priv.Equals(loaded.Priv))
	assert.True(t, original.Pub.Equals(loaded.Pub))
}

func TestSave_FileMode0600(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "priv.key")
	kp, err := identity.Generate()
	assert.NoError(t, err)
	assert.NoError(t, identity.Save(path, kp))

	info, err := os.Stat(path)
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestSave_CreatesParentDirWithMode0700(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "newdir", "priv.key")
	kp, err := identity.Generate()
	assert.NoError(t, err)
	assert.NoError(t, identity.Save(path, kp))

	info, err := os.Stat(filepath.Dir(path))
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestLoad_RejectsTooPermissiveMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not meaningful on Windows; Load skips the check there")
	}
	t.Parallel()

	path := filepath.Join(t.TempDir(), "priv.key")
	kp, err := identity.Generate()
	assert.NoError(t, err)
	assert.NoError(t, identity.Save(path, kp))

	// Loosen the mode behind Save's back.
	assert.NoError(t, os.Chmod(path, 0o644))

	_, err = identity.Load(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mode")
}

func TestLoad_MissingFileError(t *testing.T) {
	t.Parallel()

	_, err := identity.Load(filepath.Join(t.TempDir(), "absent.key"))
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}
