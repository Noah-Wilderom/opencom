package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
)

func newCallCmd() *cobra.Command {
	var background bool
	cmd := &cobra.Command{
		Use:   "call <target>",
		Short: "Place a call to a friend (foreground; Ctrl+C hangs up). Use --background to detach.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return runCallStart(cmd, args[0], background)
		},
	}
	cmd.Flags().BoolVar(&background, "background", false,
		"initiate the call and return immediately; the call lives in the daemon. "+
			"Re-attach later with `opencom call attach <call-id>`")
	cmd.AddCommand(newCallListCmd())
	cmd.AddCommand(newCallAcceptCmd())
	cmd.AddCommand(newCallAttachCmd())
	cmd.AddCommand(newCallHangupCmd())
	cmd.AddCommand(newCallMuteCmd())
	cmd.AddCommand(newCallUnmuteCmd())
	return cmd
}

// newCallMuteCmd builds `opencom call mute <call-id>`. Sends a one-shot
// IPC request; the daemon stops transmitting audio for the call and
// notifies the peer over the audio-control stream.
func newCallMuteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mute <call-id>",
		Short: "Mute outbound audio for a call",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			if err := c.Call(ctx, "calls.action",
				methods.CallsActionParams{CallID: args[0], Action: "mute"}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Muted %s\n", args[0])
			return nil
		},
	}
}

// newCallUnmuteCmd builds `opencom call unmute <call-id>`.
func newCallUnmuteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unmute <call-id>",
		Short: "Unmute outbound audio for a call",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			c, err := dialDaemonOrStart(ctx)
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			if err := c.Call(ctx, "calls.action",
				methods.CallsActionParams{CallID: args[0], Action: "unmute"}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Unmuted %s\n", args[0])
			return nil
		},
	}
}

func runCallStart(cmd *cobra.Command, target string, background bool) error {
	c, err := dialDaemonOrStart(cmd.Context())
	if err != nil {
		return fmt.Errorf("connecting to daemon: %w", err)
	}

	sub, err := c.Subscribe(cmd.Context(), "calls.start", methods.CallsStartParams{Target: target})
	if err != nil {
		c.Close()
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Calling %s...\n", target)

	if background {
		// Drain the first event to learn the call ID, print it, then
		// detach. Caller can re-attach later with `call attach <id>`.
		// Note: the daemon-side subscription stays open as long as the
		// IPC connection lives; closing the client cleanly tears it
		// down, which is fine — the call session lives in the daemon
		// independently of any client subscription.
		callID, err := waitForCallID(cmd.Context(), sub, 5*time.Second)
		sub.Close()
		c.Close()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Backgrounded. Call ID: %s\n", callID)
		fmt.Fprintf(out, "  attach: opencom call attach %s\n", callID)
		fmt.Fprintf(out, "  hang up: opencom call hangup %s\n", callID)
		return nil
	}

	defer c.Close()
	defer sub.Close()
	return streamCallEvents(cmd.Context(), c, out, sub, "")
}

// streamCallEvents consumes call state-change events until the call
// ends or the context is canceled. On Ctrl+C with a known callID, it
// sends a best-effort hangup so the remote peer sees the call end.
//
// initialCallID is "" for `call <name>` (we learn it from the first
// event) and the caller-provided ID for `attach`/`accept`.
func streamCallEvents(
	ctx context.Context,
	c *ipc.Client,
	out io.Writer,
	sub *ipc.Subscription,
	initialCallID string,
) error {
	callID := initialCallID
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

// waitForCallID blocks until the first state event with a non-empty
// SessionID arrives or timeout expires. Used by `--background` mode
// to print the call ID before detaching.
func waitForCallID(ctx context.Context, sub *ipc.Subscription, timeout time.Duration) (string, error) {
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		select {
		case <-deadline.Done():
			return "", fmt.Errorf("timed out waiting for call ID")
		case ev, ok := <-sub.Events:
			if !ok {
				return "", errors.New("subscription closed before first event")
			}
			var change struct {
				SessionID string `json:"session_id"`
			}
			if err := decodeEventData(ev, &change); err != nil {
				continue
			}
			if change.SessionID != "" {
				return change.SessionID, nil
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
			c, err := dialDaemonOrStart(ctx)
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
			fmt.Fprintln(tw, "CALL ID\tSTATE\tDIRECTION\tREMOTE\tMUTE\tAUDIO\tRX-LVL\tTX-LVL")
			for _, c := range res.Calls {
				mute := "no"
				if c.Muted {
					mute = "yes"
				} else if c.PeerMuted {
					mute = "peer"
				}
				audioStatus := c.AudioOK
				if audioStatus == "" {
					audioStatus = "-"
				}
				rx := "-∞"
				if c.RxLevelDB > -100 {
					rx = fmt.Sprintf("%d dB", c.RxLevelDB)
				}
				tx := "-∞"
				if c.TxLevelDB > -100 {
					tx = fmt.Sprintf("%d dB", c.TxLevelDB)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					c.CallID, c.State, c.Direction, c.Remote, mute, audioStatus, rx, tx)
			}
			return tw.Flush()
		},
	}
}

func newCallAcceptCmd() *cobra.Command {
	var background bool
	cmd := &cobra.Command{
		Use:   "accept <call-id>",
		Short: "Accept an inbound call (foreground; Ctrl+C hangs up). Use --background to detach.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			callID := args[0]
			out := cmd.OutOrStdout()

			c, err := dialDaemonOrStart(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}

			if background {
				ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
				defer cancel()
				defer c.Close()
				if err := c.Call(ctx, "calls.action",
					methods.CallsActionParams{CallID: callID, Action: "accept"}, nil); err != nil {
					return err
				}
				fmt.Fprintf(out, "Accepted %s (backgrounded)\n", callID)
				fmt.Fprintf(out, "  attach: opencom call attach %s\n", callID)
				fmt.Fprintf(out, "  hang up: opencom call hangup %s\n", callID)
				return nil
			}

			// Foreground: subscribe to state events FIRST so we don't
			// miss the [connected] transition that fires on accept,
			// then send the accept action, then stream until ended.
			sub, err := c.Subscribe(cmd.Context(), "calls.attach", methods.CallsAttachParams{CallID: callID})
			if err != nil {
				c.Close()
				return err
			}
			defer c.Close()
			defer sub.Close()

			actx, acancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			err = c.Call(actx, "calls.action",
				methods.CallsActionParams{CallID: callID, Action: "accept"}, nil)
			acancel()
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Accepted %s\n", callID)

			return streamCallEvents(cmd.Context(), c, out, sub, callID)
		},
	}
	cmd.Flags().BoolVar(&background, "background", false,
		"send accept and exit; the call lives in the daemon. "+
			"Re-attach later with `opencom call attach <call-id>`")
	return cmd
}

func newCallAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <call-id>",
		Short: "Attach to an in-progress call's state stream (foreground; Ctrl+C hangs up).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			callID := args[0]
			c, err := dialDaemonOrStart(cmd.Context())
			if err != nil {
				return fmt.Errorf("connecting to daemon: %w", err)
			}
			defer c.Close()
			sub, err := c.Subscribe(cmd.Context(), "calls.attach", methods.CallsAttachParams{CallID: callID})
			if err != nil {
				return err
			}
			defer sub.Close()
			fmt.Fprintf(cmd.OutOrStdout(), "Attached to %s\n", callID)
			return streamCallEvents(cmd.Context(), c, cmd.OutOrStdout(), sub, callID)
		},
	}
	return cmd
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
			c, err := dialDaemonOrStart(ctx)
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
