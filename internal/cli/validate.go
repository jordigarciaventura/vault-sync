package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/config"
)

func newValidateCommand(a *app) *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the configuration without contacting a backend",
		Long:  "Validate the manifest using the same parsing and validation rules as pull and push.\nNo provider CLI, authentication or remote access is required.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			selected := path
			if selected == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				selected, err = config.Find(wd)
				if err != nil {
					return err
				}
			}
			cfg, err := config.Load(selected, config.Options{KnownBackends: a.registry.Types()})
			if err != nil {
				return err
			}
			b, err := a.registry.New(backend.Config{Type: cfg.Backend.Type, Account: cfg.Backend.Account, Region: cfg.Backend.Region, Environment: cfg.Backend.Environment, Server: cfg.Backend.Server, NonInteractive: true})
			if err != nil {
				return err
			}
			defer func() { _ = b.Close() }()
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Configuration valid: %s (%s).\n", cfg.Path, plural(len(cfg.Files)))
			return nil
		},
	}
	cmd.Flags().StringVarP(&path, "config", "c", "", "path to the manifest (default: ./"+config.DefaultFileName+", searched upwards)")
	return cmd
}
