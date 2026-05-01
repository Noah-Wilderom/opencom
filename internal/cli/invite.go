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
			expiresIn := time.Until(time.Unix(resp.ExpiresAt, 0)).Round(time.Minute)

			if resp.DHTPublishWarning == "" {
				// DHT publish succeeded — short code works as the headline.
				fmt.Fprintf(out, "Invite code: %s\n", resp.Code)
				fmt.Fprintf(out, "Or URL:      %s\n", resp.URL)
				fmt.Fprintf(out, "Expires in:  %s\n", expiresIn)
				fmt.Fprintln(out)
				fmt.Fprintf(out, "Share this with a friend. They run:\n")
				fmt.Fprintf(out, "  opencom add %s\n", resp.Code)
				fmt.Fprintf(out, "    or\n")
				fmt.Fprintf(out, "  opencom add '%s'\n", resp.URL)
			} else {
				// DHT publish failed — short code is not redeemable until
				// DHT recovers; lead with the URL.
				fmt.Fprintf(out, "Invite URL:  %s\n", resp.URL)
				fmt.Fprintf(out, "Expires in:  %s\n", expiresIn)
				fmt.Fprintln(out)
				fmt.Fprintf(out, "Note: short code (%s) couldn't be published to the DHT\n",
					resp.Code)
				fmt.Fprintf(out, "      (%s).\n", resp.DHTPublishWarning)
				fmt.Fprintf(out, "      The URL form above is self-contained and works without DHT.\n")
				fmt.Fprintln(out)
				fmt.Fprintf(out, "Share the URL with a friend. They run:\n")
				fmt.Fprintf(out, "  opencom add '%s'\n", resp.URL)
			}
			if len(resp.ReachableAddrs) == 0 {
				fmt.Fprintln(out)
				fmt.Fprintln(out, "⚠  No cross-network address yet — AutoRelay hasn't reserved a")
				fmt.Fprintln(out, "   relay slot, and your daemon has no directly-routable public")
				fmt.Fprintln(out, "   address. The URL above will only work for friends on the")
				fmt.Fprintln(out, "   same LAN (mDNS) until reachability improves.")
				fmt.Fprintln(out, "   Run `opencom status` in ~30s; if reachability shows")
				fmt.Fprintln(out, "   \"directly reachable\" or \"via relay\", re-run `opencom invite`")
				fmt.Fprintln(out, "   to get a cross-network-redeemable URL.")
			}
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
