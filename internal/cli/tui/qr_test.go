package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderASCIIQR_ContainsBlockChars(t *testing.T) {
	t.Parallel()
	out, err := renderASCIIQR("opencom://test")
	assert.NoError(t, err)
	assert.NotEmpty(t, out)
	assert.True(t, strings.ContainsAny(out, "█▀▄"),
		"output should contain QR block glyphs")
}

func TestRenderASCIIQR_RejectsOversizedPayload(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("X", 4096) // way over QR Medium-EC capacity
	_, err := renderASCIIQR(huge)
	assert.Error(t, err)
}
