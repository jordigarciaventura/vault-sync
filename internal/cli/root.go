// Package cli wires the cobra command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/backend/bitwarden"
	"github.com/jordigarciaventura/vault-sync/internal/backend/infisical"
	"github.com/jordigarciaventura/vault-sync/internal/updater"
	"github.com/jordigarciaventura/vault-sync/internal/version"
)

const updateCheckCommand = "__update-check"

// app holds dependencies shared by commands; tests replace them.
type app struct {
	registry *backend.Registry
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
}

// Registry returns the backends compiled into vault-sync.
func Registry() *backend.Registry {
	r := backend.NewRegistry()
	r.Register(bitwarden.Type, bitwarden.New)
	r.Register(infisical.Type, infisical.New)
	return r
}

func newRootCommand(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:   "vault-sync",
		Short: "Restore project files from a secrets provider",
		Long: `vault-sync synchronizes the files listed in a project's secrets.yaml with a
remote secrets provider (Bitwarden or Infisical).

Typical workflow after cloning a repository:

  vault-sync pull`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Current(),
	}
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.SetVersionTemplate("vault-sync {{.Version}}\n")
	root.CompletionOptions.HiddenDefaultCmd = true

	root.AddCommand(
		newInitCommand(a),
		newPullCommand(a),
		newPushCommand(a),
		newLoginCommand(a),
		newLogoutCommand(a),
		newVersionCommand(a),
		newSelfUpgradeCommand(a),
		newUpdateCheckCommand(),
	)
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute(ctx context.Context, args []string) int {
	a := &app{registry: Registry(), stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
	root := newRootCommand(a)
	root.SetArgs(args)

	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	notify := sub != updateCheckCommand && sub != "self-upgrade"

	var latest updater.Release
	current := version.Current()
	if notify && updater.Enabled(current) {
		checker := updater.NewChecker(current)
		var fresh bool
		latest, fresh = checker.Cached()
		if !fresh {
			spawnBackgroundCheck()
		}
	}

	err := root.ExecuteContext(ctx)

	if notify && updater.Newer(current, latest.Version) && isTerminal(os.Stderr) {
		_, _ = fmt.Fprintf(a.stderr, "\n%s", updater.Notice(current, latest.Version))
	}

	if err != nil {
		if !errors.Is(err, errReported) {
			_, _ = fmt.Fprintf(a.stderr, "Error: %v\n", err)
		}
		return 1
	}
	return 0
}

// errReported signals that the command already printed its failure summary.
var errReported = errors.New("reported")

// spawnBackgroundCheck refreshes the update cache in a detached process so the
// current command never waits on the network.
func spawnBackgroundCheck() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, updateCheckCommand)
	if err := cmd.Start(); err != nil {
		return
	}
	_ = cmd.Process.Release()
}

func newUpdateCheckCommand() *cobra.Command {
	return &cobra.Command{
		Use:    updateCheckCommand,
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			current := version.Current()
			if !updater.Enabled(current) {
				return nil
			}
			// Failures are silent: this runs detached from any terminal.
			_, _ = updater.NewChecker(current).Refresh(cmd.Context())
			return nil
		},
	}
}

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func newLogger(w io.Writer, verbose bool) *slog.Logger {
	if !verbose {
		return slog.New(slog.DiscardHandler)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
}
