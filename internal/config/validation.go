package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// ValidationError aggregates every problem found in a manifest so the user can
// fix them in one go.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return "invalid config: " + e.Problems[0]
	}
	return "invalid config:\n  - " + strings.Join(e.Problems, "\n  - ")
}

func validate(raw *rawConfig, root string, opts Options) (*Config, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("config root %q must be absolute", root)
	}
	root = filepath.Clean(root)

	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	cfg := &Config{Root: root}

	switch {
	case raw.Backend == nil:
		add("backend: section is required")
	case strings.TrimSpace(raw.Backend.Type) == "":
		add("backend.type: is required")
	default:
		typ := strings.TrimSpace(raw.Backend.Type)
		if len(opts.KnownBackends) > 0 && !slices.Contains(opts.KnownBackends, typ) {
			add("backend.type: unknown backend %q (supported: %s)", typ, strings.Join(opts.KnownBackends, ", "))
		}
		cfg.Backend = BackendConfig{
			Type:        typ,
			Account:     strings.TrimSpace(raw.Backend.Account),
			Environment: strings.TrimSpace(raw.Backend.Environment),
			Server:      strings.TrimSpace(raw.Backend.Server),
		}
	}

	project := raw.Project
	switch {
	case strings.TrimSpace(project) == "":
		add("project: is required")
	case project != strings.TrimSpace(project):
		add("project: must not have leading or trailing whitespace")
	case strings.IndexFunc(project, unicode.IsControl) >= 0:
		add("project: must not contain control characters")
	default:
		cfg.Project = project
	}

	if len(raw.Files) == 0 {
		add("files: at least one file is required")
	}

	names := map[string]int{}
	dests := map[string]int{}
	for i, rf := range raw.Files {
		prefix := fmt.Sprintf("files[%d]", i)
		f, err := parseFile(rf, root)
		if err != nil {
			add("%s: %v", prefix, err)
			continue
		}
		if j, dup := names[f.Name]; dup {
			add("%s: path %q duplicates files[%d]", prefix, rf.Path, j)
			continue
		}
		if j, dup := dests[f.Destination]; dup {
			add("%s: destination %q duplicates files[%d]", prefix, f.Destination, j)
			continue
		}
		names[f.Name] = i
		dests[f.Destination] = i
		cfg.Files = append(cfg.Files, f)
	}

	if len(problems) > 0 {
		return nil, &ValidationError{Problems: problems}
	}
	return cfg, nil
}

func parseFile(rf rawFile, root string) (File, error) {
	p := rf.Path
	if strings.TrimSpace(p) == "" {
		return File{}, errors.New("path: is required")
	}
	if p != strings.TrimSpace(p) {
		return File{}, fmt.Errorf("path %q: must not have leading or trailing whitespace", p)
	}
	if strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return File{}, fmt.Errorf("path %q: must not contain control characters", p)
	}
	if strings.HasSuffix(p, "/") {
		return File{}, fmt.Errorf("path %q: must be a file, not a directory", p)
	}
	if strings.HasPrefix(p, "~") {
		return File{}, fmt.Errorf("path %q: '~' is not expanded; use an absolute path", p)
	}

	slashed := filepath.ToSlash(p)
	abs := path.IsAbs(slashed)
	clean := path.Clean(slashed)
	name := strings.TrimPrefix(clean, "/")

	if name == "" || name == "." {
		return File{}, fmt.Errorf("path %q: does not name a file", p)
	}
	if !abs && (name == ".." || strings.HasPrefix(name, "../")) {
		return File{}, fmt.Errorf("path %q: relative paths must stay inside the directory containing %s", p, DefaultFileName)
	}
	if abs && slices.Contains(strings.Split(slashed, "/"), "..") {
		return File{}, fmt.Errorf("path %q: absolute paths must not contain '..'", p)
	}

	dest := filepath.FromSlash(clean)
	if !abs {
		dest = filepath.Join(root, dest)
	}

	perm, err := ParsePermissions(rf.Permissions)
	if err != nil {
		return File{}, fmt.Errorf("permissions %q: %w", rf.Permissions, err)
	}

	return File{
		Path:        p,
		Name:        name,
		Destination: dest,
		Absolute:    abs,
		Permissions: perm,
	}, nil
}

// ParsePermissions parses an octal Unix permission string such as "0600" or
// "640". An empty string yields DefaultPermissions.
func ParsePermissions(s string) (fs.FileMode, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultPermissions, nil
	}
	digits := strings.TrimPrefix(strings.TrimPrefix(s, "0o"), "0O")
	if len(digits) < 3 || len(digits) > 4 {
		return 0, errors.New("must be an octal mode such as \"0600\"")
	}
	v, err := strconv.ParseUint(digits, 8, 32)
	if err != nil {
		return 0, errors.New("must be an octal mode such as \"0600\"")
	}
	if v > 0o777 {
		return 0, errors.New("setuid, setgid and sticky bits are not allowed")
	}
	if v&0o400 == 0 {
		return 0, errors.New("owner must be able to read the file")
	}
	return fs.FileMode(v), nil
}
