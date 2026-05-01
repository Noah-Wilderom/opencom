package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"opencom/internal/config"
	"opencom/internal/ipc"
)

// daemonProbeTimeout caps how long EnsureDaemonRunning waits for a
// freshly-spawned daemon to come up. The daemon's startup is dominated
// by libp2p host construction, which is sub-second in normal conditions;
// 3s leaves comfortable headroom for slower hardware.
const daemonProbeTimeout = 3 * time.Second

// EnsureDaemonRunning verifies the daemon's IPC path is accepting
// connections. If not, it spawns a detached background daemon process
// and polls the IPC path until the daemon comes up.
//
// Returns nil if a daemon is already running or starts successfully
// within the probe timeout. Returns an error if spawning fails or the
// new daemon doesn't come up in time.
func EnsureDaemonRunning(ctx context.Context) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return fmt.Errorf("resolving paths: %w", err)
	}
	if ipc.PathReachable(ctx, paths.SocketPath) {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating opencom binary: %w", err)
	}
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	if err := spawnDaemon(exe, paths.LogFile); err != nil {
		return fmt.Errorf("spawning daemon: %w", err)
	}

	deadline := time.Now().Add(daemonProbeTimeout)
	for time.Now().Before(deadline) {
		if ipc.PathReachable(ctx, paths.SocketPath) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("daemon did not start within timeout")
}
