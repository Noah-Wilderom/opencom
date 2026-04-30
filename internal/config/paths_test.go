package config_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/config"
)

func TestDefaultPaths_ContainsOpencomDirSegment(t *testing.T) {
	t.Parallel()

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	assert.Contains(t, p.ConfigDir, "opencom")
	assert.Contains(t, p.StateDir, "opencom")
}

func TestDefaultPaths_DerivedPathsAreUnderTheirRoots(t *testing.T) {
	t.Parallel()

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(p.ConfigDir, "config.yaml"), p.ConfigFile)
	assert.Equal(t, filepath.Join(p.ConfigDir, "priv.key"), p.PrivateKey)
	assert.Equal(t, filepath.Join(p.ConfigDir, "friends.json"), p.FriendsFile)
	assert.Equal(t, filepath.Join(p.StateDir, "peerstore"), p.Peerstore)
	assert.Equal(t, filepath.Join(p.StateDir, "daemon.log"), p.LogFile)
}

func TestDefaultPaths_SocketPathHonoursXDGRuntimeDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG_RUNTIME_DIR is not used on Windows")
	}
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "opencom.sock"), p.SocketPath)
}

func TestDefaultPaths_SocketPathFallsBackWhenXDGRuntimeDirUnset(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses a named pipe path, exercised by separate test")
	}
	t.Setenv("XDG_RUNTIME_DIR", "")

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	assert.NotEqual(t, "", p.SocketPath)
	assert.True(t, strings.HasSuffix(p.SocketPath, "opencom.sock"))
}

func TestDefaultPaths_WindowsSocketPathIsNamedPipe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only")
	}
	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(p.SocketPath, `\\.\pipe\opencom-`))
}

func TestDefaultPaths_RereadsHomeOnEachCall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses USERPROFILE; equivalent behaviour is OS-specific")
	}
	a := t.TempDir()
	t.Setenv("HOME", a)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	p1, err := config.DefaultPaths()
	assert.NoError(t, err)

	b := t.TempDir()
	t.Setenv("HOME", b)
	p2, err := config.DefaultPaths()
	assert.NoError(t, err)

	assert.NotEqual(t, p1.ConfigDir, p2.ConfigDir)
}
