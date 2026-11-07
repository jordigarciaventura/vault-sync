package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jordigarciaventura/vault-sync/internal/updater"
	"github.com/jordigarciaventura/vault-sync/internal/version"
)

func newVersionCommand(a *app) *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the installed version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "vault-sync %s\n", version.Current())
			if verbose {
				if version.Commit != "" {
					_, _ = fmt.Fprintf(out, "commit: %s\n", version.Commit)
				}
				if version.Date != "" {
					_, _ = fmt.Fprintf(out, "built:  %s\n", version.Date)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "also print commit and build date")
	return cmd
}

func newSelfUpgradeCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "self-upgrade",
		Short: "Upgrade vault-sync to the latest stable release",
		Long: `Upgrade vault-sync to the latest stable GitHub release.

Homebrew installations are upgraded with "brew upgrade"; other installations
are replaced in place after verifying the published SHA-256 checksum.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u := &updater.Upgrader{Checker: updater.NewChecker(version.Current()), Out: cmd.OutOrStdout()}
			return u.Upgrade(cmd.Context())
		},
	}
}
