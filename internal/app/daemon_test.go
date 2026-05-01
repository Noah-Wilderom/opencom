//go:build unix

package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"opencom/internal/app"
	"opencom/internal/config"
	"opencom/internal/identity"
	"opencom/internal/ipc"
)

// makeOptions builds a minimal app.Options pointed at a temp directory.
func makeOptions(t *testing.T) app.Options {
	t.Helper()
	root := t.TempDir()
	paths := config.Paths{
		ConfigDir:   root,
		StateDir:    filepath.Join(root, "state"),
		ConfigFile:  filepath.Join(root, "config.yaml"),
		PrivateKey:  filepath.Join(root, "priv.key"),
		FriendsFile: filepath.Join(root, "friends.json"),
		SocketPath:  filepath.Join(root, "opencom.sock"),
		Peerstore:   filepath.Join(root, "state", "peerstore"),
		LogFile:     filepath.Join(root, "state", "daemon.log"),
	}
	assert.NoError(t, os.MkdirAll(paths.StateDir, 0o700))

	kp, err := identity.Generate()
	assert.NoError(t, err)

	cfg := config.Default()
	cfg.User.Name = "Tester"

	return app.Options{
		Paths:        paths,
		Config:       cfg,
		Identity:     kp,
		Log:          zap.NewNop(),
		Version:      "test",
		StartedAt:    time.Now().UTC(),
		DisableAudio: true, // tests don't open real audio devices
	}
}

func TestRun_ListensAndAcceptsConnections(t *testing.T) {
	opts := makeOptions(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, opts) }()

	// Wait for socket to appear (Run does setup work synchronously, but
	// the listener bind happens on its goroutine).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(opts.Paths.SocketPath); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	c, err := ipc.Dial(dialCtx, opts.Paths.SocketPath)
	assert.NoError(t, err)
	if c != nil {
		c.Close()
	}

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func TestRun_FailsWhenIPCPathHeld(t *testing.T) {
	opts := makeOptions(t)

	// Hold the IPC path with a real listener so app.Run cannot bind it.
	// The new single-instance contract is "ipc.Listen succeeds → we are
	// the only daemon" — no separate file lock.
	assert.NoError(t, os.MkdirAll(filepath.Dir(opts.Paths.SocketPath), 0o700))
	ln, err := ipc.Listen(opts.Paths.SocketPath)
	assert.NoError(t, err)
	defer ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err = app.Run(ctx, opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "listening")
}

func TestRun_RemovesStaleSocketBeforeListening(t *testing.T) {
	opts := makeOptions(t)

	// Plant a stale socket file (a regular file, not a real socket).
	assert.NoError(t, os.WriteFile(opts.Paths.SocketPath, []byte{}, 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, opts) }()

	// Run should succeed and the socket should now be a Unix socket; we
	// detect that by being able to dial it.
	dialed := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		dialCtx, dcancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		c, err := ipc.Dial(dialCtx, opts.Paths.SocketPath)
		dcancel()
		if err == nil {
			c.Close()
			dialed = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.True(t, dialed, "stale socket should have been replaced and dialable")

	cancel()
	assert.NoError(t, <-done)
}

func TestRun_SocketModeIs0600(t *testing.T) {
	opts := makeOptions(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, opts) }()

	checked := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(opts.Paths.SocketPath); err == nil {
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			checked = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assert.True(t, checked, "socket should appear within 2s")

	cancel()
	<-done
}

func TestRun_CreatesSocketParentDir(t *testing.T) {
	opts := makeOptions(t)
	// Place the socket inside a not-yet-existing subdirectory; Run should
	// MkdirAll its parent before binding.
	root := filepath.Dir(opts.Paths.SocketPath)
	opts.Paths.SocketPath = filepath.Join(root, "nested", "runtime", "opencom.sock")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, opts) }()

	appeared := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(opts.Paths.SocketPath); err == nil {
			appeared = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assert.True(t, appeared, "socket should appear inside auto-created parent dir")

	info, err := os.Stat(filepath.Dir(opts.Paths.SocketPath))
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	cancel()
	<-done
}
