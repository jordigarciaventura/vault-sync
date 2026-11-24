package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/config"
	"github.com/jordigarciaventura/vault-sync/internal/filesystem"
)

// manifestPermissions is used for a manifest created by init: it holds no
// secrets and is meant to be committed.
const manifestPermissions fs.FileMode = 0o644

func newInitCommand(a *app) *cobra.Command {
	var force, nonInteractive bool
	var dest, backendType, project string
	var files []string
	var settings manifestBackend
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a secrets.yaml interactively or from flags",
		Long: `Log in to a provider, create an empty remote container and save its location
in secrets.yaml. Answer questions about the backend settings, project or
bucket name, and files to synchronize. Existing accessible containers are reused.

Use --non-interactive with --backend, --project and repeated --file flags
to create a manifest without prompts. --config selects the output path.

The manifest is validated before it is written. An existing file is only
replaced after confirmation (or with --force). Initialization authenticates and
may create remote resources, but never uploads files. For S3, select a bucket
name and region; AWS browser login starts when credentials are unavailable.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if nonInteractive {
				return a.runInitNonInteractive(cmd, force, dest, backendType, project, files, settings)
			}
			for _, name := range []string{"config", "backend", "project", "file", "account", "region", "environment", "server"} {
				if cmd.Flags().Changed(name) {
					return fmt.Errorf("--%s requires --non-interactive", name)
				}
			}
			return a.runInit(cmd, force)
		},
	}
	cmd.Flags().StringVar(&settings.Account, "account", "", "expected provider account (AWS account ID for S3)")
	cmd.Flags().StringVar(&settings.Region, "region", "", "S3 bucket region (default us-east-1)")
	cmd.Flags().StringVar(&settings.Environment, "environment", "", "provider environment (Infisical default dev)")
	cmd.Flags().StringVar(&settings.Server, "server", "", "provider instance URL (Bitwarden or Infisical)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "overwrite an existing manifest without asking")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "create a manifest from flags without prompting")
	cmd.Flags().StringVarP(&dest, "config", "c", config.DefaultFileName, "path of the manifest to create")
	cmd.Flags().StringVarP(&backendType, "backend", "b", "", "backend to use (required with --non-interactive)")
	cmd.Flags().StringVar(&project, "project", "", "project name (required with --non-interactive)")
	cmd.Flags().StringArrayVar(&files, "file", nil, "file or directory to synchronize; repeat for each entry (required with --non-interactive)")
	return cmd
}

func (a *app) runInit(cmd *cobra.Command, force bool) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	p := &prompter{in: bufio.NewReader(a.stdin), out: out}
	types := a.registry.Types()
	if len(types) == 0 {
		return errors.New("no backends are available")
	}

	// File name.
	var dest string
	for {
		name, err := p.ask(ctx, "Manifest file", config.DefaultFileName)
		if err != nil {
			return err
		}
		if dest, err = filepath.Abs(name); err != nil {
			return err
		}
		info, err := os.Stat(dest)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			p.warn("%s is a directory; choose a file name", name)
			continue
		}
		if force {
			break
		}
		ok, err := p.confirm(ctx, fmt.Sprintf("%s already exists. Overwrite it?", name))
		if err != nil {
			return err
		}
		if ok {
			break
		}
	}
	root := filepath.Dir(dest)

	// Backend.
	_, _ = fmt.Fprintln(out, "\nBackends:")
	for i, t := range types {
		_, _ = fmt.Fprintf(out, "  %d) %s\n", i+1, t)
	}
	var backendType string
	for {
		answer, err := p.ask(ctx, "Backend", types[0])
		if err != nil {
			return err
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(types) {
			backendType = types[n-1]
			break
		}
		if slices.Contains(types, strings.ToLower(answer)) {
			backendType = strings.ToLower(answer)
			break
		}
		p.warn("unknown backend %q; enter a number or one of: %s", answer, strings.Join(types, ", "))
	}

	settings, err := a.initSettings(ctx, backendType, manifestBackend{}, p, false)
	if err != nil {
		return err
	}

	// Project.
	var project string
	_, _ = fmt.Fprintln(out)
	for {
		answer, err := p.ask(ctx, "Container name (bucket/project/folder[/prefix]) in "+backendType, filepath.Base(root))
		if err != nil {
			return err
		}
		m := manifest{Backend: settings, Project: answer, Files: []manifestFile{{Path: ".env"}}}
		if problems := m.problems(root, types); len(problems) > 0 {
			p.warn("%s", strings.Join(problems, "; "))
			continue
		}
		project = answer
		break
	}

	// Files.
	m := manifest{Backend: settings, Project: project}
	_, _ = fmt.Fprintf(out, "\nFiles to sync, one per line, relative to %s or absolute.\nLeave empty to finish.\n", displayDir(root))
	for {
		def := ""
		if len(m.Files) == 0 {
			def = ".env"
		}
		answer, err := p.ask(ctx, fmt.Sprintf("File %d", len(m.Files)+1), def)
		if err != nil {
			return err
		}
		if answer == "" {
			break
		}
		candidate := m
		candidate.Files = append(slices.Clone(m.Files), manifestFile{Path: answer})
		if problems := candidate.problems(root, types); len(problems) > 0 {
			p.warn("%s", strings.Join(problems, "; "))
			continue
		}
		m = candidate
	}

	data, err := m.marshal()
	if err != nil {
		return err
	}
	if _, err := config.Parse(data, root, config.Options{KnownBackends: types}); err != nil {
		return fmt.Errorf("generated manifest is invalid: %w", err)
	}
	for {
		err := a.initializeManifest(ctx, &m, root, false)
		if err == nil {
			break
		}
		if !errors.Is(err, backend.ErrContainerUnavailable) {
			return err
		}
		p.warn("%v", err)
		for {
			name, askErr := p.ask(ctx, "Another container name (globally unique for S3)", "")
			if askErr != nil {
				return askErr
			}
			candidate := m
			candidate.Project = name
			if problems := candidate.problems(root, types); len(problems) > 0 {
				p.warn("%s", strings.Join(problems, "; "))
				continue
			}
			m = candidate
			break
		}
	}
	data, err = m.marshal()
	if err != nil {
		return err
	}
	if _, err := filesystem.WriteFile(dest, data, manifestPermissions, filesystem.Options{}); err != nil {
		return fmt.Errorf("write %s: %w; the initialized remote container is retained, retry init after fixing the local path", dest, err)
	}

	rel := displayPath(dest)
	_, _ = fmt.Fprintf(out, "\nCreated %s with %s.\n\nNext steps:\n", rel, plural(len(m.Files)))
	flag := ""
	if wd, err := os.Getwd(); err != nil || dest != filepath.Join(wd, config.DefaultFileName) {
		flag = " --config " + rel
	}
	_, _ = fmt.Fprintf(out, "  vault-sync push%s   upload the files you already have\n", flag)
	_, _ = fmt.Fprintf(out, "  vault-sync pull%s   restore them on another machine\n", flag)
	return nil
}

// manifest mirrors the YAML document init writes.
type manifest struct {
	Backend manifestBackend `yaml:"backend"`
	Project string          `yaml:"project"`
	Files   []manifestFile  `yaml:"files"`
}

type manifestBackend struct {
	Type        string `yaml:"type"`
	Account     string `yaml:"account,omitempty"`
	Environment string `yaml:"environment,omitempty"`
	Server      string `yaml:"server,omitempty"`
	Region      string `yaml:"region,omitempty"`
}

type manifestFile struct {
	Path string `yaml:"path"`
}

// marshal renders the manifest with a blank line between sections, matching
// the layout of the documented examples.
func (m manifest) marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# yaml-language-server: $schema=https://raw.githubusercontent.com/jordigarciaventura/vault-sync/main/secrets.schema.json\n")
	buf.WriteString("# vault-sync: https://github.com/jordigarciaventura/vault-sync\n\n")
	sections := []any{
		map[string]any{"backend": m.Backend},
		map[string]any{"project": m.Project},
		map[string]any{"files": m.Files},
	}
	for i, s := range sections {
		if i > 0 {
			buf.WriteByte('\n')
		}
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(s); err != nil {
			return nil, fmt.Errorf("encode manifest: %w", err)
		}
		if err := enc.Close(); err != nil {
			return nil, fmt.Errorf("encode manifest: %w", err)
		}
	}
	return buf.Bytes(), nil
}

// problems validates the manifest with the same rules pull and push use.
func (m manifest) problems(root string, types []string) []string {
	data, err := m.marshal()
	if err != nil {
		return []string{err.Error()}
	}
	_, err = config.Parse(data, root, config.Options{KnownBackends: types})
	var ve *config.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ve):
		return ve.Problems
	default:
		return []string{err.Error()}
	}
}

// prompter asks questions on out and reads answers line by line.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

// ask prints label (with def as the default) and returns the trimmed answer,
// or def when the answer is empty.
func (p *prompter) ask(ctx context.Context, label, def string) (string, error) {
	if def != "" {
		_, _ = fmt.Fprintf(p.out, "%s [%s]: ", label, def)
	} else {
		_, _ = fmt.Fprintf(p.out, "%s: ", label)
	}
	line, err := p.readLine(ctx)
	if err != nil {
		return "", err
	}
	if line = strings.TrimSpace(line); line == "" {
		return def, nil
	}
	return line, nil
}

func (p *prompter) confirm(ctx context.Context, question string) (bool, error) {
	answer, err := p.ask(ctx, question+" [y/N]", "")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func (p *prompter) warn(format string, args ...any) {
	_, _ = fmt.Fprintf(p.out, "  ✗ "+format+"\n", args...)
}

// readLine reads one line, returning early when ctx is cancelled (Ctrl-C is
// delivered as a context cancellation, not as a process kill).
func (p *prompter) readLine(ctx context.Context) (string, error) {
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := p.in.ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case <-ctx.Done():
		_, _ = fmt.Fprintln(p.out)
		return "", errors.New("init cancelled; nothing was written")
	case r := <-ch:
		if r.err != nil {
			if errors.Is(r.err, io.EOF) && r.line != "" {
				return r.line, nil
			}
			if errors.Is(r.err, io.EOF) {
				_, _ = fmt.Fprintln(p.out)
				return "", errors.New("input ended before init was complete; nothing was written")
			}
			return "", fmt.Errorf("read answer: %w", r.err)
		}
		return r.line, nil
	}
}

// displayPath returns path relative to the working directory when it is
// inside it, otherwise the absolute path.
func displayPath(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(wd, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}

func displayDir(dir string) string {
	if d := displayPath(dir); d != "." {
		return d
	}
	return "the current directory"
}

func (a *app) runInitNonInteractive(cmd *cobra.Command, force bool, name, backendType, project string, files []string, settings manifestBackend) error {
	if backendType == "" || project == "" || len(files) == 0 {
		return errors.New("--non-interactive requires --backend, --project and at least one --file")
	}
	dest, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil && !force {
		return fmt.Errorf("%s already exists; use --force to overwrite it", name)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	settings, err = a.initSettings(cmd.Context(), backendType, settings, nil, true)
	if err != nil {
		return err
	}
	m := manifest{Backend: settings, Project: project}
	for _, path := range files {
		m.Files = append(m.Files, manifestFile{Path: path})
	}
	data, err := m.marshal()
	if err != nil {
		return err
	}
	if _, err := config.Parse(data, filepath.Dir(dest), config.Options{KnownBackends: a.registry.Types()}); err != nil {
		return err
	}
	if err := cmd.Context().Err(); err != nil {
		return err
	}
	if err := a.initializeManifest(cmd.Context(), &m, filepath.Dir(dest), true); err != nil {
		return err
	}
	data, err = m.marshal()
	if err != nil {
		return err
	}
	if _, err := filesystem.WriteFile(dest, data, manifestPermissions, filesystem.Options{}); err != nil {
		return fmt.Errorf("write %s: %w; the initialized remote container is retained, retry init after fixing the local path", dest, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created %s with %s.\n", displayPath(dest), plural(len(files)))
	return nil
}

func (m manifestBackend) config(nonInteractive bool) backend.Config {
	return backend.Config{Type: m.Type, Account: m.Account, Environment: m.Environment, Server: m.Server, Region: m.Region, NonInteractive: nonInteractive}
}

func (a *app) initSettings(ctx context.Context, typ string, settings manifestBackend, p *prompter, nonInteractive bool) (manifestBackend, error) {
	settings.Type = typ
	// Inspect setup metadata without authenticating or installing a tool.
	b, err := a.registry.New(backend.Config{Type: typ, NonInteractive: nonInteractive})
	if err != nil {
		return settings, err
	}
	defer func() { _ = b.Close() }()
	initializer, ok := b.(backend.ProjectInitializer)
	if !ok {
		return settings, nil
	}
	for _, field := range initializer.SetupFields() {
		var value *string
		switch field.Key {
		case "account":
			value = &settings.Account
		case "region":
			value = &settings.Region
		case "environment":
			value = &settings.Environment
		case "server":
			value = &settings.Server
		default:
			return settings, fmt.Errorf("unsupported backend setup field %q", field.Key)
		}
		if nonInteractive {
			if *value == "" {
				*value = field.Default
			}
			continue
		}
		*value, err = p.ask(ctx, field.Label, field.Default)
		if err != nil {
			return settings, err
		}
	}
	return settings, nil
}

func (a *app) initializeManifest(ctx context.Context, m *manifest, root string, nonInteractive bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := a.registry.New(m.Backend.config(nonInteractive))
	if err != nil {
		return err
	}
	defer func() { _ = b.Close() }()
	initializer, ok := b.(backend.ProjectInitializer)
	if !ok {
		return nil
	}
	setup, err := initializer.InitializeProject(ctx, m.Project)
	if err != nil {
		return fmt.Errorf("initialize %s container: %w", b.Name(), err)
	}
	m.Project = setup.Project
	m.Backend = manifestBackend{Type: setup.Config.Type, Account: setup.Config.Account, Environment: setup.Config.Environment, Server: setup.Config.Server, Region: setup.Config.Region}
	// Provider-returned locations must also satisfy manifest validation.
	if problems := m.problems(root, a.registry.Types()); len(problems) > 0 {
		return fmt.Errorf("initialized container has invalid configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}
