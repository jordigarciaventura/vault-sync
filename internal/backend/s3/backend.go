// Package s3 implements file storage in Amazon S3 through the official AWS CLI.
// The project selects an existing bucket and optional object key prefix.
package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/toolinstall"
)

// Type is the backend identifier in secrets.yaml.
const Type = "s3"

// BinaryEnv overrides the AWS CLI executable.
const BinaryEnv = "VAULT_SYNC_AWS_PATH"

// Tool describes the AWS CLI. AWS does not publish GitHub binary releases;
// automatic installation uses Homebrew, with manual installation elsewhere.
var Tool = toolinstall.Tool{
	Name: "AWS CLI", Binary: "aws", PathEnv: BinaryEnv,
	BrewFormula: "awscli", ManualInstall: "the official AWS CLI installer (https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)",
}

type runner func(context.Context, []byte, ...string) ([]byte, error)

// Backend stores exact file bytes as S3 objects.
type Backend struct {
	run           runner
	account       string
	region        string
	interactive   func() bool
	login         func(context.Context) error
	authenticated bool
}

var (
	_ backend.Backend    = (*Backend)(nil)
	_ backend.FileLister = (*Backend)(nil)
)

// New validates configuration without installing tools or authenticating.
func New(cfg backend.Config) (backend.Backend, error) {
	if cfg.Region != "" && !regionPattern.MatchString(cfg.Region) {
		return nil, errors.New("backend.region: use an AWS region such as us-east-1")
	}
	if cfg.Environment != "" {
		return nil, errors.New("backend.environment is not supported by S3; set AWS_REGION or AWS_DEFAULT_REGION instead")
	}
	if cfg.Server != "" {
		return nil, errors.New("backend.server is not supported by S3; remove it to use Amazon S3")
	}
	var binary string
	resolve := func(ctx context.Context) (string, error) {
		if binary == "" {
			var err error
			binary, err = toolinstall.Find(ctx, Tool)
			if err != nil {
				return "", err
			}
		}
		return binary, nil
	}
	return &Backend{account: cfg.Account, region: cfg.Region,
		interactive: func() bool { return !cfg.NonInteractive && term.IsTerminal(int(os.Stdin.Fd())) },
		login: func(ctx context.Context) error {
			bin, err := resolve(ctx)
			if err != nil {
				return err
			}
			// aws login handles browser authentication and stores credentials itself.
			args := []string{"login"}
			for _, key := range []string{"sso_session", "sso_start_url"} {
				probe := exec.CommandContext(ctx, bin, "configure", "get", key)
				probe.Stdout, probe.Stderr = io.Discard, io.Discard
				if probe.Run() == nil {
					args = []string{"sso", "login"}
					break
				}
			}
			if cfg.Region != "" {
				args = append(args, "--region", cfg.Region)
			}
			cmd := exec.CommandContext(ctx, bin, args...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
			if err := cmd.Run(); err != nil {
				return errors.New("AWS login failed; use AWS CLI v2.32 or newer, or run `aws sso login` for an SSO profile")
			}
			return nil
		}, run: func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
			if binary == "" {
				var err error
				binary, err = toolinstall.Find(ctx, Tool)
				if err != nil {
					return nil, err
				}
			}
			if cfg.Region != "" {
				args = append(args, "--region", cfg.Region)
			}
			cmd := exec.CommandContext(ctx, binary, append(args, "--output", "json", "--no-cli-pager", "--no-cli-auto-prompt")...)
			cmd.Env = append(os.Environ(), "AWS_CLI_AUTO_PROMPT=off", "AWS_PAGER=")
			cmd.Stdin = bytes.NewReader(input)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, requestError(stderr.String())
			}
			return stdout.Bytes(), nil
		}}, nil
}

// Name implements backend.Backend.
func (b *Backend) Name() string { return "Amazon S3" }

// Close discards cached authentication state.
func (b *Backend) Close() error { b.authenticated = false; return nil }

// Authenticate verifies AWS credentials and the optional expected AWS account ID.
func (b *Backend) Authenticate(ctx context.Context) error {
	if b.authenticated {
		return nil
	}
	data, err := b.run(ctx, nil, "sts", "get-caller-identity")
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && b.interactive != nil && b.interactive() && b.login != nil {
		if loginErr := b.login(ctx); loginErr != nil {
			return fmt.Errorf("%w: %w", backend.ErrAuthentication, loginErr)
		}
		data, err = b.run(ctx, nil, "sts", "get-caller-identity")
	}
	if err != nil {
		return fmt.Errorf("%w: %w", backend.ErrAuthentication, err)
	}
	var identity struct{ Account string }
	if err := json.Unmarshal(data, &identity); err != nil || identity.Account == "" {
		return fmt.Errorf("%w: invalid AWS identity response", backend.ErrAuthentication)
	}
	if b.account != "" && identity.Account != b.account {
		return fmt.Errorf("%w: AWS account differs from backend.account; select the correct AWS_PROFILE or credentials", backend.ErrAuthentication)
	}
	b.account = identity.Account
	b.authenticated = true
	return nil
}

func location(project, filename string) (bucket, key string, err error) {
	bucket, prefix, err := backend.SplitProject(project)
	if err != nil {
		return "", "", err
	}
	if _, _, err := backend.SplitProject(filename); err != nil {
		return "", "", fmt.Errorf("filename: %w", err)
	}
	key = filename
	if prefix != "" {
		key = prefix + "/" + filename
	}
	return bucket, key, nil
}

// ResolveFile downloads the exact object selected by its key.
func (b *Backend) ResolveFile(ctx context.Context, project, filename string) (io.ReadCloser, error) {
	bucket, key, err := location(project, filename)
	if err != nil {
		return nil, err
	}
	if err := b.Authenticate(ctx); err != nil {
		return nil, err
	}
	data, err := b.run(ctx, nil, "s3", "cp", "s3://"+bucket+"/"+key, "-", "--only-show-errors")
	if errors.Is(err, backend.ErrNotFound) {
		return nil, backend.NotFoundError(project, filename, "check the object key and bucket")
	}
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// StoreFile uploads through stdin, keeping file contents out of process arguments.
func (b *Backend) StoreFile(ctx context.Context, project, filename string, content []byte) error {
	bucket, key, err := location(project, filename)
	if err != nil {
		return err
	}
	if err := b.Authenticate(ctx); err != nil {
		return err
	}
	_, err = b.run(ctx, content, "s3", "cp", "-", "s3://"+bucket+"/"+key, "--only-show-errors")
	return err
}

// ListFiles recursively lists logical filenames under a directory. The AWS CLI
// automatically paginates list-objects-v2; JSON output combines all pages.
func (b *Backend) ListFiles(ctx context.Context, project, directory string) ([]string, error) {
	bucket, prefix, err := backend.SplitProject(project)
	if err != nil {
		return nil, err
	}
	if directory != "" && directory != "." {
		if _, _, err := backend.SplitProject(directory); err != nil {
			return nil, err
		}
	}
	base := ""
	if prefix != "" {
		base = prefix + "/"
	}
	query := base
	if directory != "" && directory != "." {
		query += directory + "/"
	}
	if err := b.Authenticate(ctx); err != nil {
		return nil, err
	}
	data, err := b.run(ctx, nil, "s3api", "list-objects-v2", "--bucket", bucket, "--prefix", query)
	if err != nil {
		return nil, err
	}
	var result struct{ Contents []struct{ Key string } }
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, errors.New("invalid S3 object listing response")
	}
	names := []string{}
	for _, object := range result.Contents {
		if !strings.HasPrefix(object.Key, query) || strings.HasSuffix(object.Key, "/") {
			continue
		}
		name := strings.TrimPrefix(object.Key, base)
		if _, _, err := backend.SplitProject(name); err != nil {
			return nil, errors.New("S3 listing contains an unsafe object key; rename it before syncing")
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}
