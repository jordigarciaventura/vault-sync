// Package sync orchestrates pulling files from and pushing files to a backend.
// It depends only on the provider-independent backend interface.
package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/config"
	"github.com/jordigarciaventura/vault-sync/internal/filesystem"
)

// MaxFileSize bounds how much data a single resource may contain.
const MaxFileSize = 64 << 20

// Action is what happened (or would happen, in dry-run mode) to a file.
type Action string

// Possible actions.
const (
	ActionCreated   Action = "created"
	ActionUpdated   Action = "updated"
	ActionUnchanged Action = "unchanged"
	ActionPushed    Action = "pushed"
	ActionFailed    Action = "failed"
)

// Result describes the outcome for one file. Err never contains secret values.
type Result struct {
	File   config.File
	Action Action
	DryRun bool
	Err    error
}

// Options control a run.
type Options struct {
	DryRun bool
	// Report, when set, is called after each file is processed.
	Report func(Result)
}

// ErrSomeFilesFailed is returned when at least one file could not be processed.
var ErrSomeFilesFailed = errors.New("one or more files failed")

// Service runs pull and push operations against a backend.
type Service struct {
	Backend backend.Backend
	Logger  *slog.Logger
}

func (s *Service) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Logger
}

// Pull retrieves every configured file from the backend and writes it to its
// destination. Processing continues after per-file failures; the returned
// error wraps ErrSomeFilesFailed if any file failed.
func (s *Service) Pull(ctx context.Context, cfg *config.Config, opts Options) ([]Result, error) {
	if err := s.Backend.Authenticate(ctx); err != nil {
		return nil, err
	}
	return s.each(cfg, opts, func(f config.File) (Action, error) {
		return s.pullOne(ctx, cfg, f, opts.DryRun)
	})
}

// Push uploads every configured local file to the backend.
func (s *Service) Push(ctx context.Context, cfg *config.Config, opts Options) ([]Result, error) {
	if err := s.Backend.Authenticate(ctx); err != nil {
		return nil, err
	}
	return s.each(cfg, opts, func(f config.File) (Action, error) {
		return s.pushOne(ctx, cfg, f, opts.DryRun)
	})
}

func (s *Service) each(cfg *config.Config, opts Options, fn func(config.File) (Action, error)) ([]Result, error) {
	results := make([]Result, 0, len(cfg.Files))
	failed := 0
	for _, f := range cfg.Files {
		action, err := fn(f)
		if err != nil {
			action = ActionFailed
			failed++
		}
		r := Result{File: f, Action: action, DryRun: opts.DryRun, Err: err}
		results = append(results, r)
		if opts.Report != nil {
			opts.Report(r)
		}
	}
	if failed > 0 {
		return results, fmt.Errorf("%w: %d of %d", ErrSomeFilesFailed, failed, len(cfg.Files))
	}
	return results, nil
}

func (s *Service) pullOne(ctx context.Context, cfg *config.Config, f config.File, dryRun bool) (Action, error) {
	log := s.logger().With("file", f.Name)
	fsOpts := filesystem.Options{}
	if !f.Absolute {
		fsOpts.ConfineTo = cfg.Root
	}

	log.Debug("resolving resource", "project", cfg.Project)
	rc, err := s.Backend.ResolveFile(ctx, cfg.Project, f.Name)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()

	if dryRun {
		status, err := filesystem.Inspect(f.Destination, fsOpts)
		if err != nil {
			return "", err
		}
		return statusAction(status), nil
	}

	data, err := readLimited(rc)
	if err != nil {
		return "", fmt.Errorf("read %q from backend: %w", f.Name, err)
	}
	log.Debug("writing file", "destination", f.Destination, "bytes", len(data), "permissions", fmt.Sprintf("%04o", f.Permissions))
	status, err := filesystem.WriteFile(f.Destination, data, f.Permissions, fsOpts)
	if err != nil {
		return "", fmt.Errorf("write %s: %w", f.Destination, err)
	}
	return statusAction(status), nil
}

func (s *Service) pushOne(ctx context.Context, cfg *config.Config, f config.File, dryRun bool) (Action, error) {
	info, err := os.Lstat(f.Destination)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("local file %s does not exist", f.Destination)
		}
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("local file %s is not a regular file", f.Destination)
	}
	if info.Size() > MaxFileSize {
		return "", fmt.Errorf("local file %s exceeds the %d MiB limit", f.Destination, MaxFileSize>>20)
	}
	if dryRun {
		return ActionPushed, nil
	}
	data, err := os.ReadFile(f.Destination)
	if err != nil {
		return "", err
	}
	s.logger().Debug("storing resource", "file", f.Name, "project", cfg.Project, "bytes", len(data))
	if err := s.Backend.StoreFile(ctx, cfg.Project, f.Name, data); err != nil {
		return "", err
	}
	return ActionPushed, nil
}

func readLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("resource exceeds the %d MiB limit", MaxFileSize>>20)
	}
	return data, nil
}

func statusAction(s filesystem.Status) Action {
	switch s {
	case filesystem.Created:
		return ActionCreated
	case filesystem.Updated:
		return ActionUpdated
	default:
		return ActionUnchanged
	}
}
