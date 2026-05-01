package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	kardianosservice "github.com/kardianos/service"
	"github.com/spf13/cobra"

	"opencom/internal/ipc/methods"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show consolidated daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			var resp methods.DaemonStatusSummaryResult
			if err := c.Call(ctx, "daemon.status_summary", nil, &resp); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Identity        : %s\n", resp.Identity.PeerID)
			uptime := time.Since(resp.Identity.StartedAt).Round(time.Second)
			fmt.Fprintf(out, "Daemon          : running (%s uptime)\n", uptime)
			fmt.Fprintf(out, "Reachability    : %s\n", resp.Identity.Reachability)

			// Service status: query kardianos client-side since the
			// daemon doesn't know its own service-installation state.
			if svc, _, svcErr := buildService(); svcErr == nil {
				st, stErr := svc.Status()
				switch {
				case stErr != nil && (isNotInstalled(stErr) || errors.Is(stErr, kardianosservice.ErrNotInstalled)):
					fmt.Fprintln(out, "Service         : not installed")
				case stErr != nil:
					fmt.Fprintf(out, "Service         : unknown (%v)\n", stErr)
				case st == kardianosservice.StatusRunning:
					fmt.Fprintln(out, "Service         : installed (running)")
				case st == kardianosservice.StatusStopped:
					fmt.Fprintln(out, "Service         : installed (stopped)")
				default:
					fmt.Fprintln(out, "Service         : installed (state unknown)")
				}
			}

			if len(resp.Friends) == 0 {
				fmt.Fprintln(out, "Friends         : 0")
			} else {
				fmt.Fprintf(out, "Friends         : %d\n", len(resp.Friends))
				for _, f := range resp.Friends {
					state := "offline"
					if f.Online {
						state = "online"
					} else if !f.LastSeen.IsZero() {
						state = fmt.Sprintf("last seen %s ago",
							time.Since(f.LastSeen).Round(time.Minute))
					}
					name := f.Name
					if name == "" {
						name = "(unnamed)"
					}
					fmt.Fprintf(out, "  - %s (%s)\n", name, state)
				}
			}

			fmt.Fprintf(out, "Active calls    : %d\n", len(resp.Calls))

			active := make([]methods.InviteListEntry, 0, len(resp.Invites))
			for _, e := range resp.Invites {
				if e.Active {
					active = append(active, e)
				}
			}
			if len(active) == 0 {
				fmt.Fprintln(out, "Active invites  : 0")
			} else {
				fmt.Fprintf(out, "Active invites  : %d\n", len(active))
				for _, e := range active {
					expIn := time.Until(e.ExpiresAt).Round(time.Minute)
					if expIn < 0 {
						expIn = 0
					}
					fmt.Fprintf(out, "  - %s (expires in %s)\n", e.Pretty, expIn)
				}
			}

			if len(resp.Identity.ListenAddrs) > 0 {
				fmt.Fprintln(out, "Listen          :")
				for _, a := range resp.Identity.ListenAddrs {
					fmt.Fprintf(out, "  %s\n", a)
				}
			}
			return nil
		},
	}
}
