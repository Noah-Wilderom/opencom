package tui_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/cli/tui"
)

func TestRun_ZeroValueOptionsPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { _ = tui.Run(tui.Options{}) },
		"Run requires Dialler to be non-nil")
}
