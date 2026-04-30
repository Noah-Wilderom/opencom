package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"opencom/internal/app"
	"opencom/internal/config"
	"opencom/internal/identity"
	"opencom/internal/ipc"
	"opencom/internal/ipc/methods"
	openlog "opencom/internal/log"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the opencom daemon process",
	}
	cmd.AddCommand(newDaemonStartCmd())
	cmd.AddCommand(newDaemonStopCmd())
	cmd.AddCommand(newDaemonStatusCmd())
	return cmd
}

func newDaemonStartCmd() *cobra.Command {
	var foreground bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the opencom daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !foreground {
				return errors.New("background daemonization is not implemented yet; run with --foreground (managed start arrives in M8)")
			}
			return runDaemonStart(cmd)
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run the daemon in the foreground (required in M2)")
	return cmd
}

func runDaemonStart(cmd *cobra.Command) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return fmt.Errorf("resolving paths: %w", err)
	}
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("no config found; run `opencom init` first")
		}
		return fmt.Errorf("loading config: %w", err)
	}
	kp, err := identity.Load(paths.PrivateKey)
	if err != nil {
		return fmt.Errorf("loading identity (run `opencom init` first): %w", err)
	}

	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	// Append-only; rotation/truncation arrives in M8.
	logFile, err := os.OpenFile(paths.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer logFile.Close()

	log := openlog.New(cfg.Daemon.LogLevel, io.MultiWriter(logFile, cmd.ErrOrStderr()))
	defer func() { _ = log.Sync() }()

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Snapshot build info into Options so the global cli.Version is read once.
	return app.Run(ctx, app.Options{
		Paths:     paths,
		Config:    cfg,
		Identity:  kp,
		Log:       log,
		Version:   Version,
		StartedAt: time.Now().UTC(),
	})
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running opencom daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStop(cmd)
		},
	}
}

func runDaemonStop(cmd *cobra.Command) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
	defer cancel()

	client, err := dialDaemon(ctx)
	if err != nil {
		return fmt.Errorf("connecting to daemon (is it running?): %w", err)
	}
	defer client.Close()

	var resp map[string]string
	if err := client.Call(ctx, "daemon.shutdown", nil, &resp); err != nil {
		return fmt.Errorf("daemon.shutdown: %w", err)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "daemon: stopping")
	return nil
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the daemon's current status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStatus(cmd)
		},
	}
}

func runDaemonStatus(cmd *cobra.Command) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
	defer cancel()

	client, err := dialDaemon(ctx)
	if err != nil {
		fmt.Fprintln(cmd.OutOrStdout(), "daemon: not running")
		return nil
	}
	defer client.Close()

	var status methods.DaemonStatusResult
	if err := client.Call(ctx, "daemon.status", nil, &status); err != nil {
		return fmt.Errorf("daemon.status: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "daemon: running")
	fmt.Fprintf(out, "version : %s\n", status.Version)
	fmt.Fprintf(out, "peer id : %s\n", status.PeerID)
	fmt.Fprintf(out, "started : %s\n", status.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(out, "uptime  : %s\n", time.Since(status.StartedAt).Round(time.Second))
	if status.Reachability != "" {
		fmt.Fprintf(out, "reach   : %s\n", status.Reachability)
	}
	if len(status.ListenAddrs) > 0 {
		fmt.Fprintln(out, "listen  :")
		for _, a := range status.ListenAddrs {
			fmt.Fprintf(out, "          %s\n", a)
		}
	}
	return nil
}

// dialDaemon is a small helper used by Tasks 7 and 8.
func dialDaemon(ctx context.Context) (*ipc.Client, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return nil, err
	}
	return ipc.Dial(ctx, paths.SocketPath)
}
