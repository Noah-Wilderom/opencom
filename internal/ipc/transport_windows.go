//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// Listen creates a Windows-named-pipe listener at path. The pipe ACL
// grants generic-all access only to the owner (current user).
//
// The path must be of the form \\.\pipe\<name>; opencom's
// config.Paths.SocketPath produces this on Windows.
func Listen(path string) (net.Listener, error) {
	cfg := &winio.PipeConfig{
		// "D:P(A;;GA;;;OW)" = DACL, protected, allow GenericAll to OwnerRights.
		SecurityDescriptor: "D:P(A;;GA;;;OW)",
		MessageMode:        false,
		InputBufferSize:    65536,
		OutputBufferSize:   65536,
	}
	ln, err := winio.ListenPipe(path, cfg)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", path, err)
	}
	return ln, nil
}

// dialTransport connects to a Windows-named-pipe endpoint at path.
// Honors the deadline carried by ctx (translated to a winio dial
// timeout).
func dialTransport(ctx context.Context, path string) (net.Conn, error) {
	deadline := 10 * time.Second
	if d, ok := ctx.Deadline(); ok {
		if rem := time.Until(d); rem < deadline {
			deadline = rem
		}
	}
	conn, err := winio.DialPipe(path, &deadline)
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", path, err)
	}
	return conn, nil
}
