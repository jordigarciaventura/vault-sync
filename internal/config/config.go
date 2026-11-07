// Package config loads and validates the vault-sync manifest (secrets.yaml).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultFileName is the manifest file name looked up when --config is not given.
const DefaultFileName = "secrets.yaml"

// DefaultPermissions is applied to files that do not declare permissions.
const DefaultPermissions fs.FileMode = 0o600

// Config is the parsed and validated manifest.
type Config struct {
	Backend BackendConfig
	Project string
	Files   []File

	// Root is the absolute directory containing the manifest. Relative file
	// paths are resolved against it.
	Root string
	// Path is the absolute path of the manifest itself.
	Path string
}

// BackendConfig holds the provider-specific section of the manifest. It is the
// only part of the configuration that knows about a particular backend.
type BackendConfig struct {
	Type    string
	Account string
	// Environment selects a provider environment (e.g. "dev" or "prod") for
	// backends that have them. Empty means the backend's default.
	Environment string
	// Server is the provider instance (e.g. a self-hosted URL) for backends
	// that support several. Empty means the backend's default.
	Server string
}

// File is a single file entry of the manifest.
type File struct {
	// Path is the path exactly as written in the manifest.
	Path string
	// Name is the logical, provider-independent resource identifier
	// (a clean, slash-separated path without a leading slash).
	Name string
	// Destination is the absolute local path the file is written to.
	Destination string
	// Absolute reports whether Path was written as an absolute path.
	Absolute bool
	// Permissions is the Unix mode applied after writing.
	Permissions fs.FileMode
}

// rawConfig mirrors the YAML document.
type rawConfig struct {
	Backend *rawBackend `yaml:"backend"`
	Project string      `yaml:"project"`
	Files   []rawFile   `yaml:"files"`
}

type rawBackend struct {
	Type        string `yaml:"type"`
	Account     string `yaml:"account"`
	Environment string `yaml:"environment"`
	Server      string `yaml:"server"`
}

type rawFile struct {
	Path        string `yaml:"path"`
	Permissions string `yaml:"permissions"`
}

// Options customise validation.
type Options struct {
	// KnownBackends lists the accepted backend types. When empty, any
	// non-empty type is accepted.
	KnownBackends []string
}

// Find walks up from dir looking for DefaultFileName and returns its path.
func Find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, DefaultFileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s not found in the current directory or any parent directory", DefaultFileName)
		}
		dir = parent
	}
}

// Load reads, parses and validates the manifest at path.
func Load(path string, opts Options) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("config file %s does not exist", path)
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data, filepath.Dir(abs), opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = abs
	return cfg, nil
}

// Parse parses and validates manifest contents. root is the directory relative
// paths are resolved against; it must be absolute.
func Parse(data []byte, root string, opts Options) (*Config, error) {
	var raw rawConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("config is empty")
		}
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	return validate(&raw, root, opts)
}
