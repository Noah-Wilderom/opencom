package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/config"
)

func TestLoad_MissingFileReturnsDefaultAndNotExist(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := config.Load(path)

	assert.True(t, errors.Is(err, os.ErrNotExist))
	assert.Equal(t, config.Default(), cfg)
}

func TestLoad_RoundTripsDefault(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	want := config.Default()
	want.User.Name = "Noah"

	assert.NoError(t, config.Save(path, want))

	got, err := config.Load(path)
	assert.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestLoad_PartialFileFillsDefaults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := []byte("user:\n  name: Alice\n")
	assert.NoError(t, os.WriteFile(path, yaml, 0o600))

	got, err := config.Load(path)
	assert.NoError(t, err)
	assert.Equal(t, "Alice", got.User.Name)
	assert.True(t, got.Discovery.MDNS, "missing keys should default")
}

func TestLoad_UnknownKeyReturnsError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := []byte("user:\n  name: Alice\nbogus_top_level: 42\n")
	assert.NoError(t, os.WriteFile(path, yaml, 0o600))

	_, err := config.Load(path)
	assert.Error(t, err)
}

func TestSave_CreatesParentDirsWithMode0700(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "deep", "nested", "config.yaml")

	assert.NoError(t, config.Save(nested, config.Default()))

	info, err := os.Stat(filepath.Dir(nested))
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestSave_FileMode0600(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	assert.NoError(t, config.Save(path, config.Default()))

	info, err := os.Stat(path)
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestSave_AtomicallyReplaces(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")

	a := config.Default()
	a.User.Name = "Alice"
	assert.NoError(t, config.Save(path, a))

	b := config.Default()
	b.User.Name = "Bob"
	assert.NoError(t, config.Save(path, b))

	got, err := config.Load(path)
	assert.NoError(t, err)
	assert.Equal(t, "Bob", got.User.Name)
}
