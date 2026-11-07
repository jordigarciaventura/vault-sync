// Package filesystem implements the secure, atomic file writes used by pull.
package filesystem

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DirPermissions is used for directories created by vault-sync.
const DirPermissions fs.FileMode = 0o700

// Status describes what a write did (or, for Inspect, would do).
type Status int

const (
	// Created means the destination did not exist.
	Created Status = iota + 1
	// Updated means the destination existed and its contents changed.
	Updated
	// Unchanged means the destination already had the same contents.
	Unchanged
)

func (s Status) String() string {
	switch s {
	case Created:
		return "created"
	case Updated:
		return "updated"
	case Unchanged:
		return "unchanged"
	default:
		return "unknown"
	}
}

// Options control path confinement.
type Options struct {
	// ConfineTo, when set, requires the destination's real (symlink-resolved)
	// parent directory to be inside this directory.
	ConfineTo string
}

// Inspect validates dest without modifying anything and reports whether a
// write would create or update it.
func Inspect(dest string, opts Options) (Status, error) {
	if err := checkConfinement(filepath.Dir(dest), opts.ConfineTo); err != nil {
		return 0, err
	}
	info, err := os.Lstat(dest)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Created, nil
	case err != nil:
		return 0, err
	}
	if err := checkRegular(dest, info); err != nil {
		return 0, err
	}
	return Updated, nil
}

// WriteFile atomically writes data to dest with the given permissions:
// it writes a temporary file in the destination directory, applies the
// permissions, flushes it to disk and renames it over dest. Missing parent
// directories are created with DirPermissions. On failure no partial file is
// left behind and an existing destination is untouched.
func WriteFile(dest string, data []byte, perm fs.FileMode, opts Options) (status Status, err error) {
	dir := filepath.Dir(dest)

	// Check before creating directories so MkdirAll cannot be tricked into
	// creating directories outside the root through a symlinked ancestor,
	// then again after, in case the tree changed in between.
	if err := checkConfinement(dir, opts.ConfineTo); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, DirPermissions); err != nil {
		return 0, fmt.Errorf("create directory: %w", err)
	}
	if err := checkConfinement(dir, opts.ConfineTo); err != nil {
		return 0, err
	}

	status = Created
	if info, err := os.Lstat(dest); err == nil {
		if err := checkRegular(dest, info); err != nil {
			return 0, err
		}
		status = Updated
		if existing, err := os.ReadFile(dest); err == nil && bytes.Equal(existing, data) {
			if info.Mode().Perm() != perm {
				if err := os.Chmod(dest, perm); err != nil {
					return 0, fmt.Errorf("apply permissions: %w", err)
				}
			}
			return Unchanged, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dest)+".vault-sync-*")
	if err != nil {
		return 0, fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return 0, fmt.Errorf("write temporary file: %w", err)
	}
	if err = tmp.Chmod(perm); err != nil {
		return 0, fmt.Errorf("apply permissions: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return 0, fmt.Errorf("flush temporary file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return 0, fmt.Errorf("close temporary file: %w", err)
	}
	if err = os.Rename(tmpName, dest); err != nil {
		return 0, fmt.Errorf("rename into place: %w", err)
	}
	syncDir(dir)
	return status, nil
}

func checkRegular(dest string, info fs.FileInfo) error {
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("refusing to replace symbolic link %s", dest)
	case !info.Mode().IsRegular():
		return fmt.Errorf("%s exists and is not a regular file", dest)
	}
	return nil
}

// checkConfinement verifies that dir, once symlinks are resolved, is inside
// root. dir may not exist yet; its deepest existing ancestor is checked.
func checkConfinement(dir, root string) error {
	if root == "" {
		return nil
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve root directory: %w", err)
	}
	existing := dir
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	realDir, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return fmt.Errorf("resolve destination directory: %w", err)
	}
	if !within(realDir, realRoot) {
		return fmt.Errorf("destination directory %s resolves outside the project root through a symbolic link", dir)
	}
	return nil
}

func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
