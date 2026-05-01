package cli_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"opencom/internal/cli"
)

// skipIfWindowsNoUnixSockets — Windows production code uses named
// pipes via go-winio; tests that bind raw AF_UNIX paths under
// C:\Users\... fail with "bind: invalid argument" on Windows runners
// because the test fixture isn't validating Windows production
// behavior anyway. Skip on Windows.
func skipIfWindowsNoUnixSockets(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses named pipes via go-winio; skipping unix-socket test fixture")
	}
}

func TestEnsureDaemonRunning_NoOpWhenAlreadyListening(t *testing.T) {
	skipIfWindowsNoUnixSockets(t)
	withTempPaths(t)

	// Start a stub listener at the daemon's socket path so EnsureDaemonRunning
	// thinks the daemon is up. EnsureDaemonRunning should not spawn anything
	// and should return nil immediately.
	root := os.Getenv("XDG_RUNTIME_DIR")
	assert.NoError(t, os.MkdirAll(root, 0o700))
	sock := filepath.Join(root, "opencom.sock")

	ln, err := net.Listen("unix", sock)
	assert.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err = cli.EnsureDaemonRunning(ctx)
	assert.NoError(t, err, "should be a no-op when daemon is already listening")
}
