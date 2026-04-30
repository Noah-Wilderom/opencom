package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
)

func newCallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "call <target>",
		Short: "Place a call to a friend (foreground; Ctrl+C hangs up)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return runCallStart(cmd, args[0])
		},
	}
	cmd.AddCommand(newCallListCmd())
	cmd.AddCommand(newCallAcceptCmd())
	cmd.AddCommand(newCallHangupCmd())
	return cmd
}

func runCallStart(cmd *cobra.Command, target string) error {
	c, err := dialDaemon(cmd.Context())
	if err != nil {
		return fmt.Errorf("connecting to daemon: %w", err)
	}
	defer c.Close()

	sub, err := c.Subscribe(cmd.Context(), "calls.start", methods.CallsStartParams{Target: target})
	if err != nil {
		return err
	}
	defer sub.Close()

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Calling %s...\n", target)

	var callID string
	ctx := cmd.Context()
	for {
		select {
		case <-ctx.Done():
			// User pressed Ctrl+C (or context expired). If we have a CallID,
			// send a best-effort hangup so the remote peer sees the call end.
			if callID != "" {
				hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = c.Call(hctx, "calls.action",
					methods.CallsActionParams{CallID: callID, Action: "hangup", Reason: "user requested"}, nil)
				hcancel()
			}
			// Don't surface context.Canceled as a CLI error.
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return ctx.Err()
		case ev, ok := <-sub.Events:
			if !ok {
				return nil
			}
			var change struct {
				SessionID string `json:"session_id"`
				State     string `json:"state"`
				Reason    string `json:"reason,omitempty"`
			}
			if err := decodeEventData(ev, &change); err != nil {
				continue
			}
			if callID == "" && change.SessionID != "" {
				callID = change.SessionID
			}
			if change.Reason != "" {
				fmt.Fprintf(out, "[%s] %s\n", change.State, change.Reason)
			} else {
				fmt.Fprintf(out, "[%s]\n", change.State)
			}
			if change.State == "ended" {
				return nil
			}
		}
	}
}

func decodeEventData(ev *ipc.Event, dst interface{}) error {
	if ev == nil {
		return errors.New("nil event")
	}
	return json.Unmarshal(ev.Data, dst)
}

func newCallListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active calls",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemon(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			var res methods.CallsListResult
			if err := c.Call(ctx, "calls.list", nil, &res); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(res.Calls) == 0 {
				fmt.Fprintln(out, "no active calls")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "CALL ID\tSTATE\tDIRECTION\tREMOTE")
			for _, c := range res.Calls {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.CallID, c.State, c.Direction, c.Remote)
			}
			return tw.Flush()
		},
	}
}

func newCallAcceptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "accept <call-id>",
		Short: "Accept an inbound call",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemon(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			if err := c.Call(ctx, "calls.action",
				methods.CallsActionParams{CallID: args[0], Action: "accept"}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Accepted %s\n", args[0])
			return nil
		},
	}
}

func newCallHangupCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "hangup <call-id>",
		Short: "Hang up a call",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemon(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			if err := c.Call(ctx, "calls.action",
				methods.CallsActionParams{CallID: args[0], Action: "hangup", Reason: reason}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Hung up %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "user requested", "reason string sent to peer")
	return cmd
}
