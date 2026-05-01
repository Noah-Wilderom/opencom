package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	kardianosservice "github.com/kardianos/service"
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
	var background bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the opencom daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if foreground && background {
				return errors.New("--foreground and --background are mutually exclusive")
			}
			if !foreground && !background {
				// Default: foreground, preserving M2 contract.
				foreground = true
			}
			return runDaemonStart(cmd)
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run the daemon in the foreground (blocks until interrupted)")
	cmd.Flags().BoolVar(&background, "background", false, "marker flag used by auto-spawn; the actual detachment happens in the spawning parent")
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

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
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

	// If the service is installed and running, warn the user that the
	// service manager may auto-restart the daemon after we ask it to
	// stop. To stop without restart, the user should `opencom service stop`.
	if svc, _, svcErr := buildService(); svcErr == nil {
		if st, _ := svc.Status(); st == kardianosservice.StatusRunning {
			fmt.Fprintln(cmd.ErrOrStderr(),
				"warning: opencom is installed as a service; the service manager may auto-restart the daemon.")
			fmt.Fprintln(cmd.ErrOrStderr(),
				"         To stop without restart, use `opencom service stop`.")
		}
	}

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

// dialDaemon dials the daemon's IPC path. Does NOT auto-spawn — callers
// (like `daemon status` and `daemon stop`) that should report honestly
// when no daemon is running use this directly.
func dialDaemon(ctx context.Context) (*ipc.Client, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return nil, err
	}
	return ipc.Dial(ctx, paths.SocketPath)
}

// dialDaemonOrStart ensures the daemon is running (auto-spawning if
// needed), then dials it. Used by user-facing commands that should
// "just work" when the daemon isn't already running.
func dialDaemonOrStart(ctx context.Context) (*ipc.Client, error) {
	if err := EnsureDaemonRunning(ctx); err != nil {
		return nil, fmt.Errorf("ensuring daemon is running: %w", err)
	}
	return dialDaemon(ctx)
}
