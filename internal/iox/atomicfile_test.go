package iox_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/iox"
)

func TestAtomicWriteFile_WritesContentWithMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "out.bin")
	assert.NoError(t, iox.AtomicWriteFile(path, []byte("hello"), 0o600, 0o700))

	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	info, err := os.Stat(path)
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestAtomicWriteFile_CreatesParentsWithDirMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "deep", "nested", "out.bin")
	assert.NoError(t, iox.AtomicWriteFile(path, []byte("x"), 0o600, 0o700))

	info, err := os.Stat(filepath.Dir(path))
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestAtomicWriteFile_ReplacesExisting(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "out.bin")
	assert.NoError(t, iox.AtomicWriteFile(path, []byte("first"), 0o600, 0o700))
	assert.NoError(t, iox.AtomicWriteFile(path, []byte("second"), 0o600, 0o700))

	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, "second", string(data))
}
