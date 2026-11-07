package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/config"
	"github.com/jordigarciaventura/vault-sync/internal/sync"
)

type syncFlags struct {
	config  string
	dryRun  bool
	verbose bool
}

func (f *syncFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.config, "config", "c", "", "path to the manifest (default: ./"+config.DefaultFileName+", searched upwards)")
	cmd.Flags().BoolVarP(&f.dryRun, "dry-run", "n", false, "show planned operations without changing anything")
	cmd.Flags().BoolVarP(&f.verbose, "verbose", "v", false, "print diagnostic details (never secret values)")
}

func newPullCommand(a *app) *cobra.Command {
	var flags syncFlags
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Download all configured files from the backend",
		Long: `Download every file listed in secrets.yaml and write it to its destination.

Relative paths are resolved against the directory containing secrets.yaml.
Files are written atomically with the configured permissions (default 0600).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runSync(cmd, flags, true)
		},
	}
	flags.register(cmd)
	return cmd
}

func newPushCommand(a *app) *cobra.Command {
	var flags syncFlags
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Upload all configured files to the backend",
		Long: `Upload every file listed in secrets.yaml to the backend, creating or
replacing the corresponding remote resources.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runSync(cmd, flags, false)
		},
	}
	flags.register(cmd)
	return cmd
}

func (a *app) runSync(cmd *cobra.Command, flags syncFlags, pull bool) error {
	path := flags.config
	if path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		if path, err = config.Find(wd); err != nil {
			return err
		}
	}
	cfg, err := config.Load(path, config.Options{KnownBackends: a.registry.Types()})
	if err != nil {
		return err
	}

	logger := newLogger(a.stderr, flags.verbose)
	logger.Debug("loaded config", "path", cfg.Path, "files", len(cfg.Files))

	b, err := a.registry.New(backend.Config{
		Type:        cfg.Backend.Type,
		Account:     cfg.Backend.Account,
		Environment: cfg.Backend.Environment,
		Server:      cfg.Backend.Server,
		Logger:      logger,
	})
	if err != nil {
		return err
	}
	defer func() { _ = b.Close() }()

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "vault-sync\n\nProject: %s\nBackend: %s\n", cfg.Project, b.Name())
	if flags.dryRun {
		_, _ = fmt.Fprintln(out, "Mode:    dry run (no changes will be made)")
	}
	_, _ = fmt.Fprintln(out)

	svc := &sync.Service{Backend: b, Logger: logger}
	opts := sync.Options{DryRun: flags.dryRun, Report: func(r sync.Result) { printResult(out, r) }}

	var results []sync.Result
	if pull {
		results, err = svc.Pull(cmd.Context(), cfg, opts)
	} else {
		results, err = svc.Push(cmd.Context(), cfg, opts)
	}
	if err != nil && !errors.Is(err, sync.ErrSomeFilesFailed) {
		return err
	}

	ok := 0
	for _, r := range results {
		if r.Err == nil {
			ok++
		}
	}
	verb := map[bool]string{true: "synchronized", false: "pushed"}[pull]
	_, _ = fmt.Fprintln(out)
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(out, "%s %d of %d files; %d failed.\n", capitalize(verb), ok, len(results), len(results)-ok)
		return errReported
	case flags.dryRun:
		_, _ = fmt.Fprintf(out, "Dry run complete: %s would be %s.\n", plural(ok), verb)
	default:
		_, _ = fmt.Fprintf(out, "Successfully %s %s.\n", verb, plural(ok))
	}
	return nil
}

func printResult(out io.Writer, r sync.Result) {
	if r.Err != nil {
		_, _ = fmt.Fprintf(out, "✗ %s\n    %v\n", r.File.Path, r.Err)
		return
	}
	detail := string(r.Action)
	if r.DryRun {
		detail = map[sync.Action]string{
			sync.ActionCreated:   "would create",
			sync.ActionUpdated:   "would overwrite",
			sync.ActionUnchanged: "would leave unchanged",
			sync.ActionPushed:    "would push",
		}[r.Action]
	}
	if r.Action == sync.ActionPushed && !r.DryRun {
		_, _ = fmt.Fprintf(out, "✓ %s\n", r.File.Path)
		return
	}
	_, _ = fmt.Fprintf(out, "✓ %s (%s)\n", r.File.Path, detail)
}

func plural(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
