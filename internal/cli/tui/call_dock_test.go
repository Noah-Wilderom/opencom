// internal/cli/tui/call_dock_test.go
package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCallDock_IdleRendersFooterText(t *testing.T) {
	t.Parallel()
	d := callDock{}
	out := d.View(80, 6)
	assert.Contains(t, out, "no active calls")
}

func TestCallDock_ConnectedRendersControls(t *testing.T) {
	t.Parallel()
	d := callDock{}
	d.SetState(callDockState{
		State:  "connected",
		Remote: "Bob",
		Mode:   "datagram",
		RxDB:   -10,
		TxDB:   -8,
	})
	out := d.View(80, 6)
	assert.Contains(t, out, "Bob")
	assert.Contains(t, out, "datagram")
	assert.Contains(t, out, "mute")
	assert.Contains(t, out, "hangup")
	assert.True(t, strings.Contains(out, "-10") || strings.Contains(out, "-8"),
		"audio levels should appear")
}

func TestCallDock_RingingRendersWaitState(t *testing.T) {
	t.Parallel()
	d := callDock{}
	d.SetState(callDockState{State: "ringing", Remote: "Alice"})
	out := d.View(80, 6)
	assert.Contains(t, out, "Ringing")
	assert.Contains(t, out, "Alice")
}
