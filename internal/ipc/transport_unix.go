//go:build unix

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// Listen creates a Unix-domain-socket listener at path. The socket is
// chmod'd to 0o600 so only the owning user can connect.
//
// Single-instance: if another process is currently listening on path,
// Listen returns an error ("already in use"). Stale socket files from
// unclean shutdowns are detected (no live listener responds) and
// silently removed before the new listener binds.
func Listen(path string) (net.Listener, error) {
	// If a live listener accepts a quick dial, somebody else owns this
	// path — fail with the address-in-use contract.
	if conn, err := net.DialTimeout("unix", path, 100*time.Millisecond); err == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("listening on %s: address already in use", path)
	}
	// No live listener; clean up any stale socket file.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("removing stale socket %s: %w", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod socket %s: %w", path, err)
	}
	return ln, nil
}

// dialTransport connects to a Unix-domain-socket endpoint at path.
func dialTransport(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", path, err)
	}
	return conn, nil
}

