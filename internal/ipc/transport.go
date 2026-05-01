package ipc

import (
	"context"
	"time"
)

// PathReachable returns true if a connection to path can be established
// within a short timeout. Does NOT perform the protocol handshake — for
// callers (the auto-spawn helper) that just want to know "is anyone
// listening?" without paying for a full Dial.
//
// Platform-agnostic; the per-platform dialTransport handles the actual
// transport (Unix-domain socket vs. Windows named pipe).
func PathReachable(ctx context.Context, path string) bool {
	pctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	c, err := dialTransport(pctx, path)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
