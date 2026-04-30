//go:build windows

package app

import (
	"context"
	"errors"
)

// Run is not yet implemented on Windows. The opencom daemon ships AF_UNIX
// IPC on Linux/macOS in M2; Windows named-pipe support arrives in a later
// milestone. The function is defined so the cross-platform `app.Options`
// type and the CLI command surface remain present on Windows builds.
func Run(_ context.Context, _ Options) error {
	return errors.New("opencom daemon is not yet supported on Windows; AF_UNIX IPC arrives in a later milestone")
}
