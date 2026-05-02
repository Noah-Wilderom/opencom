// internal/cli/tui/status_footer_test.go
package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatusFooter_RendersHintsAndStatus(t *testing.T) {
	t.Parallel()
	out := renderStatusFooter(statusFooterState{
		Status: statusConnected,
		Detail: "Bob",
		Width:  80,
	})
	assert.Contains(t, out, "/search")
	assert.Contains(t, out, "?help")
	assert.Contains(t, out, "q quit")
	assert.Contains(t, out, "connected to Bob")
}

func TestStatusFooter_DaemonDownVariant(t *testing.T) {
	t.Parallel()
	out := renderStatusFooter(statusFooterState{
		Status: statusDaemonDown,
		Width:  80,
	})
	assert.Contains(t, out, "daemon down")
}
