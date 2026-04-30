package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"opencom/internal/config"
	"opencom/internal/identity"
	"opencom/internal/iox"
)

func newInitCmd() *cobra.Command {
	var (
		name     string
		noPrompt bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialise the local opencom config and identity",
		Long: `Creates ~/.config/opencom (or the OS equivalent), generates an
Ed25519 libp2p keypair, writes a default config.yaml, and creates an empty
friends list. Safe to run multiple times — existing files are not touched.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, name, noPrompt)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name for your identity")
	cmd.Flags().BoolVar(&noPrompt, "no-prompt", false, "reserved; non-interactive is the default in M1")
	return cmd
}

func runInit(cmd *cobra.Command, name string, noPrompt bool) error {
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
	fmt.Fprintf(out, "✓ Created config at %s\n", paths.ConfigDir)
	fmt.Fprintf(out, "✓ Identity:    %s\n", kp.PeerID.String())
	fmt.Fprintf(out, "✓ Display:     %s\n", cfg.User.Name)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintf(out, "  opencom identity export ./me.pub.key   # share with friends\n")
	fmt.Fprintf(out, "  opencom daemon start --foreground       # start the libp2p daemon\n")
	fmt.Fprintf(out, "  opencom friends add ./alice.pub.key    # import a friend's pubkey\n")
	_ = noPrompt // reserved for future interactive prompting
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
