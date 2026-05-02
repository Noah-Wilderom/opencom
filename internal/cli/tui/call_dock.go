// internal/cli/tui/call_dock.go
package tui

import (
	"fmt"
	"strings"
	"time"
)

// callDockState bundles everything the dock renderer needs. Held as
// a value so Model can pass a snapshot.
type callDockState struct {
	State     string // "" (idle) | "calling" | "ringing" | "connecting" | "connected" | "ended"
	CallID    string
	Remote    string // friend name (or short peer-ID fallback)
	StartedAt time.Time
	Mode      string // "datagram" | "stream" | "" (none yet)
	RxDB      int
	TxDB      int
	Reason    string // ended-state reason
	Muted     bool
}

// callDock is the bottom panel that grows from a thin "no active
// calls" footer line to a 6-row dock when a call is active.
type callDock struct {
	state callDockState
}

func (d *callDock) SetState(s callDockState) { d.state = s }

func (d *callDock) State() callDockState { return d.state }

// Active reports whether the dock should currently render in
// expanded form (and consume vertical space in the parent layout).
func (d *callDock) Active() bool {
	return d.state.State != "" && d.state.State != "ended"
}

func (d *callDock) View(width, height int) string {
	if d.state.State == "" {
		return theme.dim.Render(" no active calls")
	}
	var b strings.Builder
	switch d.state.State {
	case "calling":
		fmt.Fprintf(&b, " %s Calling %s…", theme.warn.Render("●"), d.state.Remote)
	case "ringing":
		fmt.Fprintf(&b, " %s Ringing — %s", theme.warn.Render("●"), d.state.Remote)
	case "connecting":
		fmt.Fprintf(&b, " %s Connecting to %s…", theme.warn.Render("●"), d.state.Remote)
	case "connected":
		dur := time.Since(d.state.StartedAt).Round(time.Second)
		mode := d.state.Mode
		if mode == "" {
			mode = "—"
		}
		fmt.Fprintf(&b, " %s %s   %s   rx %d dB   tx %d dB",
			theme.head.Render(d.state.Remote),
			dur, theme.dim.Render(mode),
			d.state.RxDB, d.state.TxDB,
		)
		fmt.Fprintf(&b, "\n %s mute  %s hangup  %s diag  %s back",
			theme.key.Render("m"), theme.key.Render("h"),
			theme.key.Render("d"), theme.key.Render("esc"))
	case "ended":
		fmt.Fprintf(&b, " %s call ended  %s",
			theme.dim.Render("●"), theme.dim.Render(d.state.Reason))
	}
	return theme.box.Width(width).Height(height).Render(b.String())
}
