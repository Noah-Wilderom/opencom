// Package tui implements opencom's "lazy mode" terminal UI. Bare
// `opencom` invocation enters this; every subcommand still works as
// before. The TUI is a Bubble Tea program that talks to the daemon
// over the same IPC the CLI uses.
package tui

import (
	"context"
)

// Dialler returns an IPC client connected to the daemon. The TUI
// keeps the connection open for the program's lifetime; a single
// connection multiplexes friends.list calls, presence/calls
// subscriptions, etc.
//
// Production wiring uses internal/cli.dialDaemonOrStart; tests
// inject a fake.
type Dialler func(ctx context.Context) (Client, error)

// Options configures Run. All fields except Dialler are optional.
type Options struct {
	// Dialler is required: the function the TUI calls once at startup
	// to obtain its IPC client.
	Dialler Dialler
	// Clipboard, when non-nil, overrides the default OS clipboard
	// reader. Tests inject a fake; production passes nil.
	Clipboard Clipboard
	// Editor, when non-empty, overrides the editor binary used by
	// the settings modal's "open in $EDITOR". Tests inject "true"
	// (the no-op shell command); production passes "".
	Editor string
}

// Run starts the TUI. Blocks until the user quits or ctx is cancelled.
func Run(opts Options) error {
	if opts.Dialler == nil {
		panic("tui.Run: Dialler is required")
	}
	// Implementation lands in Task 8.
	return nil
}

// Client is the daemon-facing surface the TUI uses. Defined here so
// later tasks can reference it; the concrete shape lands in Task 7.
type Client interface{}

// Clipboard is the OS-clipboard surface the TUI uses. Defined here
// so later tasks can reference it; the concrete shape lands in Task 19.
type Clipboard interface{}

// WrapIPCClient adapts a real *ipc.Client into the TUI's Client
// interface. The implementation lands in Task 7; for now it returns
// nil so dispatch wiring compiles.
func WrapIPCClient(c interface{}) Client { return nil }
