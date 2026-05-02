// internal/cli/tui/modal_test.go
package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderOverlay_CentersInsideBackground(t *testing.T) {
	t.Parallel()
	bg := strings.Repeat(strings.Repeat("X", 80)+"\n", 24)
	bg = strings.TrimRight(bg, "\n")
	out := renderOverlay(bg, "MODAL", 80, 24)
	assert.Contains(t, out, "MODAL")
	// 24 lines total (23 newlines).
	assert.Equal(t, 23, strings.Count(out, "\n"))
}

func TestStripANSI_RemovesCSI(t *testing.T) {
	t.Parallel()
	in := "\x1b[31mred\x1b[0m text"
	assert.Equal(t, "red text", stripANSI(in))
}
