package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"opencom/internal/config"
	"opencom/internal/identity"
)

func newIdentityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Inspect and export your opencom identity",
	}
	cmd.AddCommand(newIdentityShowCmd())
	cmd.AddCommand(newIdentityExportCmd())
	return cmd
}

func newIdentityShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print your peer ID, fingerprint, and display name",
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := config.DefaultPaths()
			if err != nil {
				return fmt.Errorf("resolving paths: %w", err)
			}
			kp, err := identity.Load(paths.PrivateKey)
			if err != nil {
				return fmt.Errorf("loading identity (run `opencom init` first): %w", err)
			}
			cfg, err := config.Load(paths.ConfigFile)
			if err != nil {
				return fmt.Errorf("loading config (run `opencom init` first): %w", err)
			}
			id := kp.PeerID.String()
			short := id
			if len(id) > 12 {
				short = id[:12]
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Display name : %s\n", cfg.User.Name)
			fmt.Fprintf(out, "Peer ID      : %s\n", id)
			fmt.Fprintf(out, "Fingerprint  : %s…\n", short)
			return nil
		},
	}
}

func newIdentityExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export <path>",
		Short: "Write your shareable public identity to a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dest := args[0]

			paths, err := config.DefaultPaths()
			if err != nil {
				return fmt.Errorf("resolving paths: %w", err)
			}
			kp, err := identity.Load(paths.PrivateKey)
			if err != nil {
				return fmt.Errorf("loading identity (run `opencom init` first): %w", err)
			}
			cfg, err := config.Load(paths.ConfigFile)
			if err != nil {
				return fmt.Errorf("loading config (run `opencom init` first): %w", err)
			}
			pub, err := identity.Export(kp, cfg.User.Name)
			if err != nil {
				return err
			}
			if err := identity.WriteExport(dest, pub); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", dest)
			return nil
		},
	}
}
