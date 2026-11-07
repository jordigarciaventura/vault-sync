package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

type memBackend struct{ files map[string]string }

func (m *memBackend) Name() string                       { return "Memory" }
func (m *memBackend) Authenticate(context.Context) error { return nil }
func (m *memBackend) Close() error                       { return nil }
func (m *memBackend) ResolveFile(_ context.Context, p, n string) (io.ReadCloser, error) {
	c, ok := m.files[n]
	if !ok {
		return nil, backend.NotFoundError(p, n, "")
	}
	return io.NopCloser(strings.NewReader(c)), nil
}
func (m *memBackend) StoreFile(_ context.Context, _, n string, c []byte) error {
	m.files[n] = string(c)
	return nil
}

func run(t *testing.T, mem *memBackend, args ...string) (string, error) {
	t.Helper()
	return runWith(t, func(backend.Config) (backend.Backend, error) { return mem, nil }, args...)
}

func runWith(t *testing.T, f backend.Factory, args ...string) (string, error) {
	t.Helper()
	return runInput(t, f, "", args...)
}

func runInput(t *testing.T, f backend.Factory, stdin string, args ...string) (string, error) {
	t.Helper()
	r := backend.NewRegistry()
	r.Register("memory", f)
	var out bytes.Buffer
	a := &app{registry: r, stdin: strings.NewReader(stdin), stdout: &out, stderr: &out}
	root := newRootCommand(a)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func writeManifest(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "secrets.yaml")
	manifest := "backend:\n  type: memory\nproject: Demo\nfiles:\n  - path: app/.env\n  - path: secret.key\n    permissions: \"0400\"\n"
	if err := os.WriteFile(p, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPullCommand(t *testing.T) {
	dir := t.TempDir()
	manifest := writeManifest(t, dir)
	mem := &memBackend{files: map[string]string{"app/.env": "TOKEN=hunter2", "secret.key": "KEY"}}

	out, err := run(t, mem, "pull", "--config", manifest)
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	for _, want := range []string{"Project: Demo", "Backend: Memory", "✓ app/.env (created)", "Successfully synchronized 2 files."} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hunter2") {
		t.Error("secret value leaked into output")
	}
	info, err := os.Stat(filepath.Join(dir, "secret.key"))
	if err != nil || info.Mode().Perm() != 0o400 {
		t.Errorf("secret.key: %v %v", info, err)
	}
}

func TestPullCommandDryRun(t *testing.T) {
	dir := t.TempDir()
	manifest := writeManifest(t, dir)
	mem := &memBackend{files: map[string]string{"app/.env": "x", "secret.key": "y"}}
	out, err := run(t, mem, "pull", "--dry-run", "--config", manifest)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "would create") || !strings.Contains(out, "Dry run complete") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "app")); err == nil {
		t.Error("dry run created files")
	}
}

func TestPullCommandPartialFailure(t *testing.T) {
	dir := t.TempDir()
	manifest := writeManifest(t, dir)
	mem := &memBackend{files: map[string]string{"app/.env": "x"}}
	out, err := run(t, mem, "pull", "--config", manifest)
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(out, "✗ secret.key") || !strings.Contains(out, "Synchronized 1 of 2 files; 1 failed.") {
		t.Errorf("output:\n%s", out)
	}
}

func TestPushCommand(t *testing.T) {
	dir := t.TempDir()
	manifest := writeManifest(t, dir)
	_ = os.MkdirAll(filepath.Join(dir, "app"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "app", ".env"), []byte("A"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "secret.key"), []byte("B"), 0o600)
	mem := &memBackend{files: map[string]string{}}

	out, err := run(t, mem, "push", "-c", manifest)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if mem.files["app/.env"] != "A" || !strings.Contains(out, "Successfully pushed 2 files.") {
		t.Errorf("files=%v out=\n%s", mem.files, out)
	}
}

func TestInvalidConfigFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secrets.yaml")
	_ = os.WriteFile(p, []byte("backend: {type: nope}\nproject: x\nfiles: [{path: a}]\n"), 0o644)
	_, err := run(t, &memBackend{}, "pull", "--config", p)
	if err == nil || !strings.Contains(err.Error(), `unknown backend "nope"`) {
		t.Errorf("err = %v", err)
	}
}

func TestVersionAndHelp(t *testing.T) {
	out, err := run(t, &memBackend{}, "version")
	if err != nil || !strings.HasPrefix(out, "vault-sync ") {
		t.Errorf("version: %q %v", out, err)
	}
	out, err = run(t, &memBackend{})
	if err != nil || !strings.Contains(out, "Available Commands") {
		t.Errorf("help: %q %v", out, err)
	}
}
