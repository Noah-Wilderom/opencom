package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"opencom/internal/ipc/methods"
)

// Build-time variables; set with -ldflags by goreleaser.
var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// NewRootCmd returns a fresh root command. Tests construct one per-test.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "opencom",
		Short: "Peer-to-peer voice and video calling for the terminal.",
		Long: `opencom is a peer-to-peer voice and video calling application
for the terminal. Calls are end-to-end encrypted, transported over libp2p,
and require no operator-controlled infrastructure.`,
		SilenceUsage: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			maybePrintUpgradeAvailable(cmd)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cmd.Help()
			}
			return runTUI()
		},
	}

	root.AddCommand(newVersionCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newIdentityCmd())
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newFriendsCmd())
	root.AddCommand(newCallCmd())
	root.AddCommand(newServiceCmd())
	root.AddCommand(newInviteCmd())
	root.AddCommand(newAddCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newUpgradeCmd())

	return root
}

// maybePrintUpgradeAvailable runs once per CLI invocation. If the
// daemon is reachable, it queries the version.check IPC method and
// prints a one-line stderr warning when an upgrade is available.
//
// Strict no-op when:
//   - the daemon isn't running (no auto-spawn just for a version check)
//   - the running binary is "dev" (no released version to upgrade to)
//   - the subcommand is upgrade-related (already handled there)
//
// Total budget: ~200ms. Anything longer (slow socket, hung daemon)
// is silently abandoned so the user's command isn't delayed.
func maybePrintUpgradeAvailable(cmd *cobra.Command) {
	if Version == "dev" {
		return
	}
	// Skip for commands where the warning is either pointless or
	// noisy (the upgrade command itself, version-print, daemon
	// start which has its own startup output, etc).
	switch cmd.Name() {
	case "upgrade", "version", "start", "stop", "status":
		return
	}
	// Walk up the command chain to catch "daemon start" / "daemon status".
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == "upgrade" {
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	client, err := dialDaemon(ctx)
	if err != nil {
		return
	}
	defer client.Close()
	var res methods.VersionCheckResult
	if err := client.Call(ctx, "version.check", nil, &res); err != nil {
		return
	}
	if !res.UpgradeAvailable {
		return
	}
	out := cmd.ErrOrStderr()
	fmt.Fprintf(out, "⚠ opencom v%s is available (you have v%s) — run '%s upgrade' to install\n",
		res.Latest, res.Current, strings.TrimSpace(rootName(cmd)))
}

// rootName returns the binary name as the user invoked it, falling
// back to "opencom" if cobra hasn't populated it.
func rootName(cmd *cobra.Command) string {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Parent() == nil {
			return c.Name()
		}
	}
	return "opencom"
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(),
				"opencom %s (commit %s, built %s)\n",
				Version, Commit, BuildDate,
			)
			return err
		},
	}
}
