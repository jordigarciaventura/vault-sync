package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/config"
)

func memFactory(backend.Config) (backend.Backend, error) { return &memBackend{}, nil }

func TestInitWritesValidManifest(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// File name, backend, project, files: defaults where empty; one invalid
	// path and one duplicate are rejected and re-asked.
	input := "\n1\nMy Project\n\n../escape\napp/.env\n./app/.env\n/shared/x.key\n\n"
	out, err := runInput(t, memFactory, input, "init")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(dir, "secrets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "backend:\n  type: memory\n\nproject: My Project\n\nfiles:\n  - path: .env\n  - path: app/.env\n  - path: /shared/x.key\n"
	if string(data) != want {
		t.Errorf("manifest:\n%s\nwant:\n%s", data, want)
	}
	if _, err := config.Parse(data, dir, config.Options{KnownBackends: []string{"memory"}}); err != nil {
		t.Errorf("manifest does not parse: %v", err)
	}
	info, _ := os.Stat(filepath.Join(dir, "secrets.yaml"))
	if info.Mode().Perm() != manifestPermissions {
		t.Errorf("mode = %o", info.Mode().Perm())
	}
	for _, s := range []string{"must stay inside", "duplicates files[1]", "Created secrets.yaml with 3 files.", "vault-sync push   "} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}
}

func TestInitCustomFileNameAndBackendChoice(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	r := backend.NewRegistry()
	r.Register("alpha", memFactory)
	r.Register("beta", memFactory)

	var out strings.Builder
	a := &app{registry: r, stdin: strings.NewReader("conf/vault.yaml\nnope\n2\n\n.env\n\n"), stdout: &out, stderr: &out}
	root := newRootCommand(a)
	root.SetArgs([]string{"init"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "conf", "vault.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "type: beta") || !strings.Contains(string(data), "project: conf") {
		t.Errorf("manifest:\n%s", data)
	}
	for _, s := range []string{`unknown backend "nope"`, "--config conf/vault.yaml"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("output missing %q:\n%s", s, out.String())
		}
	}
}

func TestInitExistingManifest(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	p := filepath.Join(dir, "secrets.yaml")
	if err := os.WriteFile(p, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Declining the overwrite asks for another name; input then ends.
	out, err := runInput(t, memFactory, "\nn\n", "init")
	if err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if data, _ := os.ReadFile(p); string(data) != "keep" {
		t.Errorf("manifest was modified: %q", data)
	}

	// --force overwrites without asking.
	if out, err := runInput(t, memFactory, "\n\nproj\n\n\n", "init", "--force"); err != nil {
		t.Fatalf("init --force: %v\n%s", err, out)
	}
	if data, _ := os.ReadFile(p); !strings.Contains(string(data), "project: proj") {
		t.Errorf("manifest not overwritten:\n%s", data)
	}
}

func TestInitRequiresAFile(t *testing.T) {
	t.Chdir(t.TempDir())
	// Clearing the default first file is not possible: an empty answer
	// selects ".env", so the manifest always has at least one file.
	out, err := runInput(t, memFactory, "\n\np\n\n\n", "init")
	if err != nil || !strings.Contains(out, "with 1 file.") {
		t.Fatalf("err=%v\n%s", err, out)
	}
}
