package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"opencom/internal/app"
	"opencom/internal/config"
	"opencom/internal/identity"
	openlog "opencom/internal/log"
)

// serviceConfig is the kardianos/service descriptor for opencom.
//
// UserService scope (start at user login, not at boot) is required
// because the daemon will own audio/video device handles in later
// milestones — those only make sense in a logged-in user session.
func serviceConfig() *service.Config {
	return &service.Config{
		Name:        "opencom",
		DisplayName: "opencom — peer-to-peer calling",
		Description: "Background daemon for opencom calls",
		Option: service.KeyValue{
			"UserService": true,
		},
	}
}

// ServiceConfigForTest exposes the service config for tests; not for
// production use.
func ServiceConfigForTest() *service.Config { return serviceConfig() }

// opencomService is the service.Service implementation that hosts
// app.Run inside a service-manager-controlled lifecycle. Start spawns
// app.Run in a goroutine and returns immediately (per kardianos/service
// contract). Stop cancels the context and waits for app.Run to drain.
type opencomService struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan error
	logger service.Logger
}

func (s *opencomService) Start(svc service.Service) error {
	// Best-effort: get the service-manager logger so post-Start failures
	// surface via the platform's service log (Windows Event Log,
	// systemd journal, etc.) rather than dying silently.
	if log, lerr := svc.Logger(nil); lerr == nil {
		s.mu.Lock()
		s.logger = log
		s.mu.Unlock()
	}

	paths, err := config.DefaultPaths()
	if err != nil {
		return fmt.Errorf("resolving paths: %w", err)
	}
	if err := os.MkdirAll(paths.ConfigDir, 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	kp, err := identity.Load(paths.PrivateKey)
	if err != nil {
		return fmt.Errorf("loading identity: %w", err)
	}
	logFile, err := os.OpenFile(paths.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening daemon log: %w", err)
	}
	log := openlog.New(cfg.Daemon.LogLevel, logFile)

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.done = make(chan error, 1)
	logger := s.logger
	s.mu.Unlock()

	go func() {
		runErr := app.Run(ctx, app.Options{
			Paths:     paths,
			Config:    cfg,
			Identity:  kp,
			Log:       log,
			Version:   Version,
			StartedAt: time.Now().UTC(),
		})
		// Surface non-nil exits to the service manager. If Stop is in
		// flight, this still doesn't race because s.done has a buffer
		// of 1 and Stop waits on it.
		if runErr != nil && logger != nil {
			_ = logger.Errorf("opencom daemon exited with error: %v", runErr)
		}
		s.done <- runErr
	}()
	return nil
}

func (s *opencomService) Stop(svc service.Service) error {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
	return nil
}

// buildService constructs the kardianos/service handle.
func buildService() (service.Service, *opencomService, error) {
	impl := &opencomService{}
	svc, err := service.New(impl, serviceConfig())
	if err != nil {
		return nil, nil, fmt.Errorf("constructing service: %w", err)
	}
	return svc, impl, nil
}

// BuildServiceForMain is the entry point for cmd/cli/main.go. Returns a
// runnable kardianos/service.Service that delegates Start/Stop to
// opencomService (which wraps app.Run).
func BuildServiceForMain() (service.Service, *opencomService, error) {
	return buildService()
}

// newServiceCmd builds the `opencom service` command tree.
func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install and manage the opencom service",
	}
	cmd.AddCommand(newServiceInstallCmd())
	cmd.AddCommand(newServiceUninstallCmd())
	cmd.AddCommand(newServiceStatusCmd())
	cmd.AddCommand(newServiceStartCmd())
	cmd.AddCommand(newServiceStopCmd())
	return cmd
}

func newServiceInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Register opencom as a user service that starts at login",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := buildService()
			if err != nil {
				return err
			}
			if err := svc.Install(); err != nil {
				if isAlreadyInstalled(err) {
					fmt.Fprintln(cmd.OutOrStdout(), "Service already installed.")
					return nil
				}
				return fmt.Errorf("installing service: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Service installed. It will start at next user login.")
			fmt.Fprintln(cmd.OutOrStdout(), "Run `opencom service start` to start it now.")
			return nil
		},
	}
}

func newServiceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the opencom service",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := buildService()
			if err != nil {
				return err
			}
			// Best-effort stop first so uninstall doesn't fail if running.
			_ = svc.Stop()
			if err := svc.Uninstall(); err != nil {
				if isNotInstalled(err) {
					fmt.Fprintln(cmd.OutOrStdout(), "Service was not installed.")
					return nil
				}
				return fmt.Errorf("uninstalling service: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Service uninstalled.")
			return nil
		},
	}
}

func newServiceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print whether the opencom service is installed and running",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := buildService()
			if err != nil {
				return err
			}
			st, err := svc.Status()
			out := cmd.OutOrStdout()
			if err != nil {
				if isNotInstalled(err) || errors.Is(err, service.ErrNotInstalled) {
					fmt.Fprintln(out, "service: not installed")
					return nil
				}
				return fmt.Errorf("querying service status: %w", err)
			}
			switch st {
			case service.StatusRunning:
				fmt.Fprintln(out, "service: running")
			case service.StatusStopped:
				fmt.Fprintln(out, "service: installed (stopped)")
			default:
				fmt.Fprintln(out, "service: installed (state unknown)")
			}
			return nil
		},
	}
}

func newServiceStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the opencom service",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := buildService()
			if err != nil {
				return err
			}
			if err := svc.Start(); err != nil {
				return fmt.Errorf("starting service: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Service started.")
			return nil
		},
	}
}

func newServiceStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the opencom service",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := buildService()
			if err != nil {
				return err
			}
			if err := svc.Stop(); err != nil {
				return fmt.Errorf("stopping service: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Service stopped.")
			return nil
		},
	}
}

// isAlreadyInstalled / isNotInstalled — kardianos/service returns
// platform-specific errors; we match by string. service.ErrNotInstalled
// catches the common case on most platforms.
func isAlreadyInstalled(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "already exists") ||
		strings.Contains(s, "already installed") ||
		strings.Contains(s, "File exists") ||
		strings.Contains(s, "already enabled")
}

func isNotInstalled(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, service.ErrNotInstalled) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "not installed") || strings.Contains(s, "service does not exist")
}
