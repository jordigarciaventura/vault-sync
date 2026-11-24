package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func TestValidateCommand(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir)
	factory := func(cfg backend.Config) (backend.Backend, error) {
		if !cfg.NonInteractive {
			t.Fatal("validation allows prompts")
		}
		return &memBackend{}, nil
	}
	out, err := runWith(t, factory, "validate", "-c", path)
	if err != nil || !strings.Contains(out, "Configuration valid:") {
		t.Fatalf("%s: %v", out, err)
	}
	sub := filepath.Join(dir, "nested")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if _, err := runWith(t, factory, "validate"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("backend: {type: memory}\nproject: Demo\nfiles: [{path: ../escape}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runWith(t, factory, "validate"); err == nil {
		t.Fatal("invalid manifest accepted")
	}
}

func TestValidateBackendOptions(t *testing.T) {
	path := writeManifest(t, t.TempDir())
	_, err := runWith(t, func(backend.Config) (backend.Backend, error) { return nil, errors.New("invalid backend options") }, "validate", "-c", path)
	if err == nil || !strings.Contains(err.Error(), "invalid backend options") {
		t.Fatalf("error = %v", err)
	}
}

func TestInitNonInteractive(t *testing.T) {
	t.Chdir(t.TempDir())
	args := []string{"init", "--non-interactive", "--backend", "memory", "--project", "Demo", "--file", "config/", "--file", "a,b", "--config", "nested/manifest.yaml"}
	if _, err := runWith(t, memFactory, args...); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("nested/manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "path: a,b") {
		t.Fatal("file name split at comma")
	}
	if _, err := runWith(t, memFactory, args...); err == nil {
		t.Fatal("overwrote without force")
	}
	if _, err := runWith(t, memFactory, append(args, "--force")...); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{{}, {"--backend", "memory", "--project", "Demo", "--file", "../escape"}, {"--backend", "unknown", "--project", "Demo", "--file", "x"}} {
		if _, err := runWith(t, memFactory, append([]string{"init", "--non-interactive"}, extra...)...); err == nil {
			t.Fatal("invalid init accepted")
		}
	}
	if _, err := os.Stat("secrets.yaml"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid init wrote a manifest")
	}
}

func TestSyncNonInteractiveDryRun(t *testing.T) {
	for _, command := range []string{"pull", "push"} {
		t.Run(command, func(t *testing.T) {
			dir := t.TempDir()
			path := writeManifest(t, dir)
			if err := os.Mkdir(filepath.Join(dir, "app"), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"app/.env", "secret.key"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("local"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			mem := &memBackend{files: map[string]string{"app/.env": "remote", "secret.key": "remote"}}
			out, err := runWith(t, func(cfg backend.Config) (backend.Backend, error) {
				if !cfg.NonInteractive {
					t.Fatal("flag not passed to backend")
				}
				return mem, nil
			}, command, "-c", path, "--dry-run", "--non-interactive")
			if err != nil || !strings.Contains(out, "Dry run complete") {
				t.Fatalf("%s: %v", out, err)
			}
			for _, name := range []string{"app/.env", "secret.key"} {
				data, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || string(data) != "local" || mem.files[name] != "remote" {
					t.Fatal("dry run changed data")
				}
			}
		})
	}
}

func TestLoginNonInteractive(t *testing.T) {
	path := writeManifest(t, t.TempDir())
	b := &accountBackend{}
	_, err := runWith(t, func(cfg backend.Config) (backend.Backend, error) {
		if !cfg.NonInteractive {
			t.Fatal("flag not passed to backend")
		}
		return b, nil
	}, "login", "--non-interactive", "-c", path)
	if err != nil || b.logins != 1 {
		t.Fatalf("logins = %d, error = %v", b.logins, err)
	}
}
