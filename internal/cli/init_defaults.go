package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jordigarciaventura/vault-sync/internal/filesystem"
)

// Only non-secret prompt answers belong here, never provider credentials or
// provider-returned project IDs. A nil userConfigDir disables persistence.
type initDefaults struct {
	Backend   string                          `yaml:"backend"`
	Providers map[string]initProviderDefaults `yaml:"providers"`
}

type initProviderDefaults struct {
	Settings      manifestBackend `yaml:"settings"`
	ProjectPrefix string          `yaml:"project_prefix,omitempty"`
}

func (a *app) loadInitDefaults() (initDefaults, string, error) {
	var prefs initDefaults
	if a.userConfigDir == nil {
		return prefs, "", nil
	}
	dir, err := a.userConfigDir()
	if err != nil {
		return prefs, "", err
	}
	name := filepath.Join(dir, "vault-sync", "init-defaults.yaml")
	data, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return prefs, name, nil
	}
	if err != nil {
		return prefs, name, err
	}
	if err := yaml.Unmarshal(data, &prefs); err != nil {
		return initDefaults{}, name, err
	}
	return prefs, name, nil
}

func (p initDefaults) save(name, typ string, settings manifestBackend, project string) error {
	if name == "" {
		return nil
	}
	if p.Providers == nil {
		p.Providers = make(map[string]initProviderDefaults)
	}
	prefix := ""
	if i := strings.LastIndex(project, "/"); i >= 0 {
		prefix = project[:i+1]
	}
	p.Backend = typ
	p.Providers[typ] = initProviderDefaults{Settings: settings, ProjectPrefix: prefix}
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	_, err = filesystem.WriteFile(name, data, 0o600, filesystem.Options{})
	return err
}
