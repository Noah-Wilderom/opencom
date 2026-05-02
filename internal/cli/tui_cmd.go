package cli

import (
	"context"

	"opencom/internal/cli/tui"
	"opencom/internal/config"
)

// RunTUIForTest, when non-nil, replaces the production TUI runner.
// Used only by root_test to assert dispatch without booting Bubble
// Tea. Set/unset under a defer in the test.
var RunTUIForTest func() error

func runTUI() error {
	if RunTUIForTest != nil {
		return RunTUIForTest()
	}
	// Resolve the user's config-file path so the settings modal can
	// point them at it. Failure is non-fatal — the modal degrades to
	// an empty path string and the user can fix it later.
	configPath := ""
	if paths, err := config.DefaultPaths(); err == nil {
		configPath = paths.ConfigFile
	}
	return tui.Run(tui.Options{
		Dialler: func(ctx context.Context) (tui.Client, error) {
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return nil, err
			}
			return tui.WrapIPCClient(c), nil
		},
		ConfigPath: configPath,
	})
}
