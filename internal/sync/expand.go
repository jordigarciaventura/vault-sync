package sync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/config"
	"github.com/jordigarciaventura/vault-sync/internal/filesystem"
)

func (s *Service) expand(ctx context.Context, cfg *config.Config, f config.File, pull bool) ([]config.File, error) {
	directory := f.Directory
	info, statErr := os.Lstat(f.Destination)
	if statErr == nil && info.IsDir() {
		directory = true
	}
	var names []string
	discovered := false
	if !directory && pull && errors.Is(statErr, os.ErrNotExist) {
		if lister, ok := s.Backend.(backend.FileLister); ok {
			var err error
			names, err = lister.ListFiles(ctx, cfg.Project, f.Name)
			if err != nil && !errors.Is(err, backend.ErrNotFound) {
				return nil, err
			}
			if err == nil {
				for _, name := range names {
					if strings.HasPrefix(name, f.Name+"/") {
						directory, discovered = true, true
						break
					}
				}
			}
		}
	}
	if !directory {
		return []config.File{f}, nil
	}
	re, err := regexp.Compile(f.Regex)
	if err != nil {
		return nil, fmt.Errorf("invalid regex; use Go regexp syntax")
	}
	prefix := f.Name
	if prefix == "." {
		prefix = ""
	}
	if pull && !discovered {
		lister, ok := s.Backend.(backend.FileLister)
		if !ok {
			return nil, fmt.Errorf("backend does not support directory discovery")
		}
		names, err = lister.ListFiles(ctx, cfg.Project, prefix)
	} else if !pull {
		// Inspect a synthetic child to check the directory's symlink confinement.
		opts := filesystem.Options{}
		if !f.Absolute {
			opts.ConfineTo = cfg.Root
		}
		if _, err := filesystem.Inspect(filepath.Join(f.Destination, ".vault-sync-check"), opts); err != nil {
			return nil, err
		}
		err = filepath.WalkDir(f.Destination, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(f.Destination, p)
			if err != nil {
				return err
			}
			names = append(names, path.Join(prefix, filepath.ToSlash(rel)))
			return nil
		})
	}
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	var files []config.File
	for _, name := range slices.Compact(names) {
		if !fs.ValidPath(name) || strings.Contains(name, "\\") {
			return nil, fmt.Errorf("backend returned an unsafe file path")
		}
		rel := name
		if prefix != "" {
			var ok bool
			rel, ok = strings.CutPrefix(name, prefix+"/")
			if !ok {
				continue
			}
		}
		if !re.MatchString(rel) {
			continue
		}
		child := f
		child.Name = name
		child.Path = filepath.Join(f.Path, filepath.FromSlash(rel))
		child.Destination = filepath.Join(f.Destination, filepath.FromSlash(rel))
		child.Directory, child.Regex = false, ""
		files = append(files, child)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("directory %s has no matching files; check the path and regex", f.Path)
	}
	return files, nil
}
