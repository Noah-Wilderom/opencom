package cli

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"opencom/internal/ipc/methods"
)

func newFriendsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "friends",
		Short: "Manage your opencom friend list",
	}
	cmd.AddCommand(newFriendsAddCmd())
	cmd.AddCommand(newFriendsListCmd())
	cmd.AddCommand(newFriendsRemoveCmd())
	cmd.AddCommand(newFriendsRenameCmd())
	cmd.AddCommand(newFriendsShowCmd())
	return cmd
}

func newFriendsAddCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "add <key-file>",
		Short: "Add a friend from their public-key YAML file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			var entry methods.FriendsListEntry
			if err := c.Call(ctx, "friends.add",
				methods.FriendsAddParams{KeyPath: args[0], Name: name}, &entry); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added %s (%s)\n", entry.Name, entry.PeerID)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "override the display name in the key file")
	return cmd
}

func newFriendsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List your friends",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			var res methods.FriendsListResult
			if err := c.Call(ctx, "friends.list", nil, &res); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(res.Friends) == 0 {
				fmt.Fprintln(out, "no friends yet — add one with `opencom friends add ./their.pub.key`")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSTATUS\tLAST SEEN\tPEER ID")
			for _, f := range res.Friends {
				status := "offline"
				if f.Online {
					status = "online"
				}
				lastSeen := "never"
				if !f.LastSeen.IsZero() {
					lastSeen = f.LastSeen.Format(time.RFC3339)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", f.Name, status, lastSeen, shortPeerID(f.PeerID.String()))
			}
			return tw.Flush()
		},
	}
}

func newFriendsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a friend",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			if err := c.Call(ctx, "friends.remove",
				methods.FriendsRemoveParams{Name: args[0]}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", args[0])
			return nil
		},
	}
}

func newFriendsRenameCmd() *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "rename <name>",
		Short: "Rename a friend",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return fmt.Errorf("--to is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			if err := c.Call(ctx, "friends.rename",
				methods.FriendsRenameParams{Name: args[0], NewName: to}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Renamed %s -> %s\n", args[0], to)
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "new display name (required)")
	return cmd
}

func newFriendsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show full detail for a friend",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			var res methods.FriendsShowResult
			if err := c.Call(ctx, "friends.show",
				methods.FriendsShowParams{Name: args[0]}, &res); err != nil {
				return err
			}
			status := "offline"
			if res.Online {
				status = "online"
			}
			lastSeen := "never"
			if !res.LastSeen.IsZero() {
				lastSeen = res.LastSeen.Format(time.RFC3339)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Name        : %s\n", res.Name)
			fmt.Fprintf(out, "Peer ID     : %s\n", res.PeerID)
			fmt.Fprintf(out, "Public key  : %s\n", res.PublicKey)
			fmt.Fprintf(out, "Added at    : %s\n", res.AddedAt.Format(time.RFC3339))
			fmt.Fprintf(out, "Status      : %s\n", status)
			fmt.Fprintf(out, "Last seen   : %s\n", lastSeen)
			return nil
		},
	}
}

// shortPeerID truncates a peer ID to its first 12 + last 4 characters
// for tabular display.
func shortPeerID(id string) string {
	if len(id) <= 18 {
		return id
	}
	return id[:12] + "…" + id[len(id)-4:]
}
