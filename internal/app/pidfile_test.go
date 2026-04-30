//go:build unix

package app_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/app"
)

func TestAcquirePIDFile_CreatesFileWithCurrentPID(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pid")
	release, err := app.AcquirePIDFile(path)
	assert.NoError(t, err)
	defer release()

	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%d\n", os.Getpid()), string(data))
}

func TestAcquirePIDFile_FileMode0600(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pid")
	release, err := app.AcquirePIDFile(path)
	assert.NoError(t, err)
	defer release()

	info, err := os.Stat(path)
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestAcquirePIDFile_SecondAcquireFailsWhenHeld(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pid")
	release1, err := app.AcquirePIDFile(path)
	assert.NoError(t, err)
	defer release1()

	_, err = app.AcquirePIDFile(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already running")
}

func TestAcquirePIDFile_ReclaimsStaleLock(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pid")
	// Use a PID that is virtually guaranteed not to exist. PID 0 is the
	// scheduler; very large PID values get rejected by FindProcess but pass
	// the existence check on Linux. We pick a PID that's very unlikely to
	// be live: 0x7fffffff (bigger than any normal PID limit).
	assert.NoError(t, os.WriteFile(path, []byte("2147483646\n"), 0o600))

	release, err := app.AcquirePIDFile(path)
	assert.NoError(t, err)
	defer release()

	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%d\n", os.Getpid()), string(data))
}

func TestAcquirePIDFile_ReleaseRemovesFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "pid")
	release, err := app.AcquirePIDFile(path)
	assert.NoError(t, err)
	assert.NoError(t, release())

	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}
