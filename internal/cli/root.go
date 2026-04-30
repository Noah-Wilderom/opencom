package cli

import (
	"fmt"

	"github.com/spf13/cobra"
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
	}

	root.AddCommand(newVersionCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newIdentityCmd())
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newFriendsCmd())
	root.AddCommand(newCallCmd())

	return root
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
