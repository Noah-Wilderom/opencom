package cli_test

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/cli"
	"opencom/internal/config"
	"opencom/internal/identity"
)

// withTempPaths runs fn with HOME pointed at a temp dir and XDG_*_HOME set
// so DefaultPaths() resolves under the temp dir on Linux.
func withTempPaths(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "runtime"))
	return root
}

// startDaemonStub creates a Unix-socket listener at the daemon's socket
// path so EnsureDaemonRunning's PathReachable check returns true,
// preventing the auto-spawn from trying to fork the test binary. The
// stub does not speak the IPC protocol — IPC dials will fail their
// hello handshake — but the code paths that only need "is the daemon
// up?" pass.
func startDaemonStub(t *testing.T) {
	t.Helper()
	skipIfWindowsNoUnixSockets(t)
	root := os.Getenv("XDG_RUNTIME_DIR")
	assert.NoError(t, os.MkdirAll(root, 0o700))
	sock := filepath.Join(root, "opencom.sock")
	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
}

func TestInit_CreatesAllExpectedFiles(t *testing.T) {
	withTempPaths(t)
	startDaemonStub(t)

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "--name=Alice", "--no-prompt"})
	assert.NoError(t, root.Execute())

	p, err := config.DefaultPaths()
	assert.NoError(t, err)

	for _, path := range []string{p.ConfigFile, p.PrivateKey, p.FriendsFile} {
		_, statErr := os.Stat(path)
		assert.NoError(t, statErr, "expected %s to exist", path)
	}
}

func TestInit_PrivateKeyMode0600(t *testing.T) {
	withTempPaths(t)
	startDaemonStub(t)

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "--name=Alice", "--no-prompt"})
	assert.NoError(t, root.Execute())

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	info, err := os.Stat(p.PrivateKey)
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestInit_StoresDisplayName(t *testing.T) {
	withTempPaths(t)
	startDaemonStub(t)

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "--name=Alice", "--no-prompt"})
	assert.NoError(t, root.Execute())

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	cfg, err := config.Load(p.ConfigFile)
	assert.NoError(t, err)
	assert.Equal(t, "Alice", cfg.User.Name)
}

func TestInit_PrintsPeerID(t *testing.T) {
	withTempPaths(t)
	startDaemonStub(t)

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "--name=Alice", "--no-prompt"})
	assert.NoError(t, root.Execute())

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	kp, err := identity.Load(p.PrivateKey)
	assert.NoError(t, err)
	assert.Contains(t, out.String(), kp.PeerID.String())
}

func TestInit_FriendsFileIsEmptyJSONArray(t *testing.T) {
	withTempPaths(t)
	startDaemonStub(t)

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "--name=Alice", "--no-prompt"})
	assert.NoError(t, root.Execute())

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	data, err := os.ReadFile(p.FriendsFile)
	assert.NoError(t, err)
	assert.Equal(t, "[]\n", string(data))
}

func TestInit_IdempotentDoesNotOverwriteExistingKey(t *testing.T) {
	withTempPaths(t)
	startDaemonStub(t)

	root1 := cli.NewRootCmd()
	root1.SetArgs([]string{"init", "--name=Alice", "--no-prompt"})
	root1.SetOut(&bytes.Buffer{})
	root1.SetErr(&bytes.Buffer{})
	assert.NoError(t, root1.Execute())

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	kp1, err := identity.Load(p.PrivateKey)
	assert.NoError(t, err)

	root2 := cli.NewRootCmd()
	root2.SetArgs([]string{"init", "--name=Alice", "--no-prompt"})
	root2.SetOut(&bytes.Buffer{})
	root2.SetErr(&bytes.Buffer{})
	assert.NoError(t, root2.Execute())

	kp2, err := identity.Load(p.PrivateKey)
	assert.NoError(t, err)
	assert.Equal(t, kp1.PeerID, kp2.PeerID, "init must not regenerate the key")
}

func TestInit_IdempotentDoesNotOverwriteName(t *testing.T) {
	withTempPaths(t)
	startDaemonStub(t)

	root1 := cli.NewRootCmd()
	root1.SetArgs([]string{"init", "--name=Alice", "--no-prompt"})
	root1.SetOut(&bytes.Buffer{})
	root1.SetErr(&bytes.Buffer{})
	assert.NoError(t, root1.Execute())

	root2 := cli.NewRootCmd()
	root2.SetArgs([]string{"init", "--name=Bob", "--no-prompt"})
	root2.SetOut(&bytes.Buffer{})
	root2.SetErr(&bytes.Buffer{})
	assert.NoError(t, root2.Execute())

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	cfg, err := config.Load(p.ConfigFile)
	assert.NoError(t, err)
	assert.Equal(t, "Alice", cfg.User.Name, "init must not overwrite an existing display name")
}

func TestInit_ConfigFileMode0600(t *testing.T) {
	withTempPaths(t)
	startDaemonStub(t)

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "--name=Alice", "--no-prompt"})
	assert.NoError(t, root.Execute())

	p, err := config.DefaultPaths()
	assert.NoError(t, err)
	info, err := os.Stat(p.ConfigFile)
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
