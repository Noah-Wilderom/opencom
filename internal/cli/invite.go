package cli

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"opencom/internal/ipc/methods"
)

func newInviteCmd() *cobra.Command {
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "Generate a one-time invite code (default) or manage active invites",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			var resp methods.InviteCreateResult
			params := methods.InviteCreateParams{}
			if ttl > 0 {
				params.TTL = ttl.String()
			}
			if err := c.Call(ctx, "invite.create", params, &resp); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Invite code: %s\n", resp.Code)
			expiresIn := time.Until(time.Unix(resp.ExpiresAt, 0)).Round(time.Minute)
			fmt.Fprintf(out, "Expires in:  %s\n", expiresIn)
			fmt.Fprintln(out)
			fmt.Fprintf(out, "Share this with a friend. They run:\n  opencom add %s\n", resp.Code)
			return nil
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "TTL (default 30m, max 30m)")
	cmd.AddCommand(newInviteListCmd())
	cmd.AddCommand(newInviteRevokeCmd())
	return cmd
}

func newInviteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show active and recent invite codes",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			var resp methods.InviteListResult
			if err := c.Call(ctx, "invite.list", nil, &resp); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(resp.Invites) == 0 {
				fmt.Fprintln(out, "no invite codes")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "CODE\tEXPIRES IN\tSTATUS")
			for _, e := range resp.Invites {
				status := "active"
				if e.Consumed {
					status = fmt.Sprintf("consumed (%s)", shortenPeerID(e.ConsumedBy))
				} else if !e.Active {
					status = "expired"
				}
				expIn := time.Until(e.ExpiresAt).Round(time.Minute)
				if expIn < 0 {
					expIn = 0
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Pretty, expIn, status)
			}
			return tw.Flush()
		},
	}
}

func newInviteRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <code>",
		Short: "Remove an active invite code",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			var resp methods.InviteRevokeResult
			if err := c.Call(ctx, "invite.revoke",
				methods.InviteRevokeParams{Code: args[0]}, &resp); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s\n", resp.Removed)
			return nil
		},
	}
}

// shortenPeerID truncates a peer ID for display.
func shortenPeerID(s string) string {
	if len(s) > 12 {
		return s[:6] + "…" + s[len(s)-6:]
	}
	return s
}
