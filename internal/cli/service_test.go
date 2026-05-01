package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"opencom/internal/cli"
)

func TestBuildService_ConfigShape(t *testing.T) {
	t.Parallel()

	cfg := cli.ServiceConfigForTest()
	assert.Equal(t, "opencom", cfg.Name)
	assert.NotEmpty(t, cfg.DisplayName)
	assert.NotEmpty(t, cfg.Description)
	// UserService scope is required because the daemon runs in the
	// user's session (future media work needs the GUI session).
	assert.Equal(t, true, cfg.Option["UserService"])
}
