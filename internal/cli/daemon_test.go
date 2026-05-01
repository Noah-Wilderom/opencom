package cli_test

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/cli"
	"opencom/internal/identity"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
)

func TestDaemonStart_ForegroundAndBackgroundAreMutuallyExclusive(t *testing.T) {
	withTempPaths(t)

	root := cli.NewRootCmd()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"daemon", "start", "--foreground", "--background"})

	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "mutually exclusive")
}

func TestDaemonStart_ForegroundErrorsWithoutInit(t *testing.T) {
	withTempPaths(t)
	// No `opencom init` was run, so config is missing.

	root := cli.NewRootCmd()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"daemon", "start", "--foreground"})

	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "init")
}

func TestDaemonHelp_DescribesSubcommands(t *testing.T) {
	t.Parallel()

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"daemon", "--help"})
	assert.NoError(t, root.Execute())

	help := strings.ToLower(out.String())
	assert.Contains(t, help, "start")
	assert.Contains(t, help, "stop")
	assert.Contains(t, help, "status")
}

// startInProcessServer launches an ipc.Server on the path that
// config.DefaultPaths().SocketPath resolves to inside this test's withTempPaths
// scope. It returns a function the test can call to stop and clean up.
func startInProcessServer(t *testing.T, kp identity.Keypair, version string, startedAt time.Time) func() {
	t.Helper()
	root := os.Getenv("XDG_RUNTIME_DIR")
	assert.NoError(t, os.MkdirAll(root, 0o700))
	sock := filepath.Join(root, "opencom.sock")

	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)
	assert.NoError(t, os.Chmod(sock, 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	s := ipc.NewServer(zap.NewNop(), version)
	s.Register("daemon.status", methods.DaemonStatus(version, kp, startedAt, nil, nil))
	go func() { _ = s.Serve(ctx, ln) }()

	return func() {
		cancel()
		_ = ln.Close()
		_ = os.Remove(sock)
	}
}

func TestDaemonStatus_PrintsRunningWhenDaemonAlive(t *testing.T) {
	withTempPaths(t)

	kp, err := identity.Generate()
	assert.NoError(t, err)
	stop := startInProcessServer(t, kp, "test-version", time.Now().UTC())
	defer stop()

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"daemon", "status"})
	assert.NoError(t, root.Execute())

	body := out.String()
	assert.Contains(t, body, "daemon: running")
	assert.Contains(t, body, "test-version")
	assert.Contains(t, body, kp.PeerID.String())
}

func TestDaemonStatus_PrintsNotRunningWhenSocketAbsent(t *testing.T) {
	withTempPaths(t)

	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"daemon", "status"})
	assert.NoError(t, root.Execute())

	assert.Contains(t, out.String(), "daemon: not running")
}

func TestDaemonStop_PrintsStoppingAndCancelsServer(t *testing.T) {
	withTempPaths(t)

	root := os.Getenv("XDG_RUNTIME_DIR")
	assert.NoError(t, os.MkdirAll(root, 0o700))
	sock := filepath.Join(root, "opencom.sock")

	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)
	assert.NoError(t, os.Chmod(sock, 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := ipc.NewServer(zap.NewNop(), "test")
	s.Register("daemon.shutdown", methods.DaemonShutdown(cancel))
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.Serve(ctx, ln) }()

	rootCmd := cli.NewRootCmd()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"daemon", "stop"})
	assert.NoError(t, rootCmd.Execute())

	assert.Contains(t, out.String(), "daemon: stopping")

	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after daemon.shutdown")
	}
}

func TestDaemonStop_ErrorsWhenDaemonNotRunning(t *testing.T) {
	withTempPaths(t)

	rootCmd := cli.NewRootCmd()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs([]string{"daemon", "stop"})

	err := rootCmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "daemon")
}
