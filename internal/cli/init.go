package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"opencom/internal/config"
	"opencom/internal/identity"
	"opencom/internal/iox"
	"opencom/internal/ipc/methods"
)

func newInitCmd() *cobra.Command {
	var (
		name        string
		noPrompt    bool
		withService bool
	)
	cmd := &cobra.Command{
		Use:   "init [<name>]",
		Short: "Initialise opencom: identity, daemon, and a fresh invite code",
		Long: `Creates ~/.config/opencom (or the OS equivalent), generates an
Ed25519 libp2p keypair, writes a default config.yaml, creates an empty
friends list, auto-starts the daemon, and prints a fresh invite code.
Safe to run multiple times — existing files are reused.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && name == "" {
				name = args[0]
			}
			return runInit(cmd, name, noPrompt, withService)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name for your identity")
	cmd.Flags().BoolVar(&noPrompt, "no-prompt", false, "reserved; non-interactive is the default")
	cmd.Flags().BoolVar(&withService, "service", false, "also run `opencom service install`")
	return cmd
}

func runInit(cmd *cobra.Command, name string, noPrompt bool, withService bool) error {
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

	kp, err := ensureKeypair(paths.PrivateKey)
	if err != nil {
		return err
	}
	cfg, err := ensureConfig(paths.ConfigFile, name)
	if err != nil {
		return err
	}
	if err := ensureFriendsFile(paths.FriendsFile); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "✓ Identity:   %s (display: %s)\n", kp.PeerID.String(), cfg.User.Name)

	// Auto-spawn the daemon if not running.
	ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
	defer cancel()
	if err := EnsureDaemonRunning(ctx); err != nil {
		fmt.Fprintf(out, "✗ Daemon failed to start: %v\n", err)
		fmt.Fprintln(out, "  Run `opencom daemon start --foreground` for diagnostics.")
		return err
	}
	fmt.Fprintln(out, "✓ Daemon:     running")

	// Optional service install.
	if withService {
		svc, _, svcErr := buildService()
		if svcErr != nil {
			fmt.Fprintf(out, "✗ Service support unavailable: %v\n", svcErr)
		} else {
			if installErr := svc.Install(); installErr == nil || isAlreadyInstalled(installErr) {
				fmt.Fprintln(out, "✓ Service:    installed (will start at next login)")
			} else {
				fmt.Fprintf(out, "✗ Service install failed: %v\n", installErr)
			}
		}
	}

	// Generate a fresh invite via IPC.
	c, err := dialDaemon(ctx)
	if err != nil {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Daemon is running but couldn't connect for invite generation.")
		fmt.Fprintln(out, "Try `opencom invite` manually.")
		return nil
	}
	defer c.Close()
	var resp methods.InviteCreateResult
	if err := c.Call(ctx, "invite.create", methods.InviteCreateParams{}, &resp); err != nil {
		fmt.Fprintf(out, "✗ Could not generate invite: %v\n", err)
		return nil
	}
	fmt.Fprintln(out, "")
	fmt.Fprintf(out, "  Invite code: %s (valid 30 min)\n", resp.Code)
	fmt.Fprintln(out, "")
	fmt.Fprintf(out, "  Share this code with a friend. They run:\n    opencom add %s\n", resp.Code)
	if !withService {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "  Tip: run `opencom service install` to keep the daemon running across reboots.")
	}
	_ = noPrompt
	return nil
}

func ensureKeypair(path string) (identity.Keypair, error) {
	if _, err := os.Stat(path); err == nil {
		return identity.Load(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return identity.Keypair{}, fmt.Errorf("checking key file: %w", err)
	}
	kp, err := identity.Generate()
	if err != nil {
		return identity.Keypair{}, err
	}
	if err := identity.Save(path, kp); err != nil {
		return identity.Keypair{}, err
	}
	return kp, nil
}

func ensureConfig(path, name string) (config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, err
	}
	if errors.Is(err, os.ErrNotExist) {
		cfg = config.Default()
		if name != "" {
			cfg.User.Name = name
		}
		if err := config.Save(path, cfg); err != nil {
			return config.Config{}, err
		}
	}
	return cfg, nil
}

func ensureFriendsFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking friends file: %w", err)
	}
	return iox.AtomicWriteFile(path, []byte("[]\n"), 0o600, 0o700)
}
