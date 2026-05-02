package cli

import (
	"context"

	"opencom/internal/cli/tui"
)

// RunTUIForTest, when non-nil, replaces the production TUI runner.
// Used only by root_test to assert dispatch without booting Bubble
// Tea. Set/unset under a defer in the test.
var RunTUIForTest func() error

func runTUI() error {
	if RunTUIForTest != nil {
		return RunTUIForTest()
	}
	return tui.Run(tui.Options{
		Dialler: func(ctx context.Context) (tui.Client, error) {
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return nil, err
			}
			return tui.WrapIPCClient(c), nil
		},
	})
}
