package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"opencom/internal/ipc/methods"
)

func newAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <code-or-url>",
		Short: "Redeem an invite to add a friend",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			var resp methods.InviteRedeemResult
			if err := c.Call(ctx, "invite.redeem",
				methods.InviteRedeemParams{Code: args[0]}, &resp); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"✓ Added %s (%s) as a friend.\n",
				resp.Friend.Name, shortenPeerID(string(resp.Friend.PeerID)))
			return nil
		},
	}
}
