package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/config"
)

// mockBackend is an in-memory backend.Backend.
type mockBackend struct {
	files     map[string]string // logical name -> content
	ambiguous map[string]bool
	readErr   map[string]error
	authErr   error
	stored    map[string]string
	resolved  []string
}

func (m *mockBackend) Name() string                       { return "Mock" }
func (m *mockBackend) Authenticate(context.Context) error { return m.authErr }
func (m *mockBackend) Close() error                       { return nil }

func (m *mockBackend) ResolveFile(_ context.Context, project, name string) (io.ReadCloser, error) {
	m.resolved = append(m.resolved, name)
	if m.ambiguous[name] {
		return nil, backend.AmbiguousError(project, name, []string{"a", "b"})
	}
	if err := m.readErr[name]; err != nil {
		return io.NopCloser(&failingReader{err}), nil
	}
	c, ok := m.files[name]
	if !ok {
		return nil, backend.NotFoundError(project, name, "")
	}
	return io.NopCloser(strings.NewReader(c)), nil
}

func (m *mockBackend) StoreFile(_ context.Context, _, name string, content []byte) error {
	if m.stored == nil {
		m.stored = map[string]string{}
	}
	m.stored[name] = string(content)
	return nil
}

type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

func testConfig(t *testing.T, root string, names ...string) *config.Config {
	t.Helper()
	cfg := &config.Config{Project: "P", Root: root}
	for _, n := range names {
		dest := n
		abs := filepath.IsAbs(n)
		if !abs {
			dest = filepath.Join(root, n)
		}
		cfg.Files = append(cfg.Files, config.File{
			Path: n, Name: strings.TrimPrefix(filepath.ToSlash(filepath.Clean(n)), "/"),
			Destination: dest, Absolute: abs, Permissions: 0o600,
		})
	}
	return cfg
}

func TestPullSuccess(t *testing.T) {
	root := t.TempDir()
	absDir := t.TempDir()
	absPath := filepath.Join(absDir, "keys", "k.key")
	absName := strings.TrimPrefix(filepath.ToSlash(absPath), "/")

	m := &mockBackend{files: map[string]string{"backend/.env": "A=1", absName: "KEY"}}
	cfg := testConfig(t, root, "backend/.env", absPath)

	var reported []Result
	results, err := (&Service{Backend: m}).Pull(context.Background(), cfg, Options{Report: func(r Result) { reported = append(reported, r) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || len(reported) != 2 {
		t.Fatalf("results=%d reported=%d", len(results), len(reported))
	}
	for _, r := range results {
		if r.Action != ActionCreated {
			t.Errorf("%s: action %s", r.File.Name, r.Action)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "backend", ".env")); string(b) != "A=1" {
		t.Errorf("relative file content %q", b)
	}
	if b, _ := os.ReadFile(absPath); string(b) != "KEY" {
		t.Errorf("absolute file content %q", b)
	}

	// Second pull is a no-op.
	results, err = (&Service{Backend: m}).Pull(context.Background(), cfg, Options{})
	if err != nil || results[0].Action != ActionUnchanged {
		t.Errorf("second pull: %v %v", results[0].Action, err)
	}
}

func TestPullContinuesAfterFailures(t *testing.T) {
	root := t.TempDir()
	m := &mockBackend{
		files:     map[string]string{"ok": "1", "broken": "x"},
		ambiguous: map[string]bool{"dup": true},
		readErr:   map[string]error{"broken": errors.New("network down")},
	}
	cfg := testConfig(t, root, "missing", "dup", "broken", "ok")

	results, err := (&Service{Backend: m}).Pull(context.Background(), cfg, Options{})
	if !errors.Is(err, ErrSomeFilesFailed) {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(results[0].Err, backend.ErrNotFound) {
		t.Errorf("missing: %v", results[0].Err)
	}
	if !errors.Is(results[1].Err, backend.ErrAmbiguous) {
		t.Errorf("dup: %v", results[1].Err)
	}
	if results[2].Err == nil || !strings.Contains(results[2].Err.Error(), "network down") {
		t.Errorf("broken: %v", results[2].Err)
	}
	if results[3].Err != nil || results[3].Action != ActionCreated {
		t.Errorf("ok: %+v", results[3])
	}
	for _, n := range []string{"missing", "dup", "broken"} {
		if _, err := os.Stat(filepath.Join(root, n)); err == nil {
			t.Errorf("%s was written despite failing", n)
		}
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Errorf("unexpected files left behind: %v", entries)
	}
}

func TestPullAuthenticationError(t *testing.T) {
	m := &mockBackend{authErr: backend.ErrAuthentication}
	_, err := (&Service{Backend: m}).Pull(context.Background(), testConfig(t, t.TempDir(), "a"), Options{})
	if !errors.Is(err, backend.ErrAuthentication) {
		t.Fatalf("err = %v", err)
	}
	if len(m.resolved) != 0 {
		t.Error("resolved files despite authentication failure")
	}
}

func TestPullDryRunDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "exists"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &mockBackend{files: map[string]string{"exists": "new", "dir/new": "n"}}
	results, err := (&Service{Backend: m}).Pull(context.Background(), testConfig(t, root, "exists", "dir/new"), Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Action != ActionUpdated || results[1].Action != ActionCreated || !results[0].DryRun {
		t.Errorf("actions: %s %s", results[0].Action, results[1].Action)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "exists")); string(b) != "old" {
		t.Error("dry run modified a file")
	}
	if _, err := os.Stat(filepath.Join(root, "dir")); err == nil {
		t.Error("dry run created a directory")
	}
}

func TestPullDryRunReportsMissingRemote(t *testing.T) {
	m := &mockBackend{files: map[string]string{}}
	_, err := (&Service{Backend: m}).Pull(context.Background(), testConfig(t, t.TempDir(), "x"), Options{DryRun: true})
	if !errors.Is(err, ErrSomeFilesFailed) {
		t.Fatalf("err = %v", err)
	}
}

func TestPush(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "backend"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "backend", ".env"), []byte("A=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &mockBackend{}
	cfg := testConfig(t, root, "backend/.env", "missing")

	results, err := (&Service{Backend: m}).Push(context.Background(), cfg, Options{})
	if !errors.Is(err, ErrSomeFilesFailed) {
		t.Fatalf("err = %v", err)
	}
	if results[0].Action != ActionPushed || m.stored["backend/.env"] != "A=1" {
		t.Errorf("push result %+v stored %v", results[0], m.stored)
	}
	if results[1].Err == nil || !strings.Contains(results[1].Err.Error(), "does not exist") {
		t.Errorf("missing: %v", results[1].Err)
	}
}

func TestPushDryRunDoesNotStore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &mockBackend{}
	if _, err := (&Service{Backend: m}).Push(context.Background(), testConfig(t, root, "f"), Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(m.stored) != 0 {
		t.Error("dry run stored data")
	}
}

func (m *mockBackend) ListFiles(_ context.Context, _, _ string) ([]string, error) {
	var names []string
	for name := range m.files {
		names = append(names, name)
	}
	return names, nil
}

func TestDirectoryRoundTrip(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprint(dryRun), func(t *testing.T) {
			root := t.TempDir()
			cfg, err := config.Parse([]byte("backend: {type: mock}\nproject: P\nfiles:\n  - path: secrets/\n    regex: '\\.txt$'\n    permissions: '0640'\n"), root, config.Options{})
			if err != nil {
				t.Fatal(err)
			}
			m := &mockBackend{files: map[string]string{"secrets/a.txt": "a", "secrets/nested/b.txt": "b", "secrets/skip.bin": "skip", "secrets-other/no.txt": "no"}}
			svc := &Service{Backend: m}
			results, err := svc.Pull(context.Background(), cfg, Options{DryRun: dryRun})
			if err != nil || len(results) != 2 {
				t.Fatalf("%v %v", results, err)
			}
			if dryRun {
				if _, err := os.Stat(filepath.Join(root, "secrets")); !os.IsNotExist(err) {
					t.Fatal("dry run created directory")
				}
				return
			}
			for _, r := range results {
				info, err := os.Stat(r.File.Destination)
				if err != nil || info.Mode().Perm() != 0o640 {
					t.Fatalf("permissions: %v %v", info, err)
				}
			}
			results, err = svc.Push(context.Background(), cfg, Options{})
			if err != nil || len(results) != 2 || len(m.stored) != 2 {
				t.Fatalf("%v %v %v", results, err, m.stored)
			}
		})
	}
}

func TestDirectoryUnsafeAndEmpty(t *testing.T) {
	for _, name := range []string{"secrets/../../escape", "elsewhere/x"} {
		cfg := testConfig(t, t.TempDir(), "secrets/")
		cfg.Files[0].Directory = true
		m := &mockBackend{files: map[string]string{name: "x"}}
		_, err := (&Service{Backend: m}).Pull(context.Background(), cfg, Options{})
		if !errors.Is(err, ErrSomeFilesFailed) {
			t.Fatalf("%q: %v", name, err)
		}
	}
}

func TestDirectoryPushSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "ok"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "dir", "ok"), filepath.Join(root, "dir", "link")); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t, root, "dir")
	m := &mockBackend{}
	results, err := (&Service{Backend: m}).Push(context.Background(), cfg, Options{})
	if err != nil || len(results) != 1 || len(m.stored) != 1 {
		t.Fatalf("%v %v", results, err)
	}
}

func TestDirectoryOverlap(t *testing.T) {
	cfg := testConfig(t, t.TempDir(), "dir/", "dir/a")
	cfg.Files[0].Directory = true
	m := &mockBackend{files: map[string]string{"dir/a": "x"}}
	results, err := (&Service{Backend: m}).Pull(context.Background(), cfg, Options{})
	if !errors.Is(err, ErrSomeFilesFailed) || len(results) != 2 || results[1].Err == nil {
		t.Fatalf("%v %v", results, err)
	}
}

func TestPullDetectsMissingDirectory(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprint(dryRun), func(t *testing.T) {
			root := t.TempDir()
			cfg := testConfig(t, root, "services/worker/credentials")
			m := &mockBackend{files: map[string]string{
				"services/worker/credentials/token.txt":          "token",
				"services/worker/credentials/nested/signing.txt": "signing",
				"services/worker/credentials-other/ignored.txt":  "ignored",
			}}
			results, err := (&Service{Backend: m}).Pull(context.Background(), cfg, Options{DryRun: dryRun})
			if err != nil || len(results) != 2 {
				t.Fatalf("results=%+v err=%v", results, err)
			}
			for _, r := range results {
				if r.Action != ActionCreated || r.DryRun != dryRun {
					t.Fatalf("unexpected result: %+v", r)
				}
				data, err := os.ReadFile(r.File.Destination)
				if dryRun {
					if !os.IsNotExist(err) {
						t.Fatalf("dry run wrote file: %v", err)
					}
				} else if err != nil || string(data) != m.files[r.File.Name] {
					t.Fatalf("incorrect restored file %s: %v", r.File.Name, err)
				}
			}
		})
	}
}

func TestPullMissingPathFallsBackToFile(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			root := t.TempDir()
			m := &mockBackend{files: map[string]string{"services/worker/credentials-other/token.txt": "ignored"}}
			if exists {
				m.files["services/worker/credentials"] = "single file"
			}
			results, err := (&Service{Backend: m}).Pull(context.Background(), testConfig(t, root, "services/worker/credentials"), Options{})
			if len(results) != 1 {
				t.Fatalf("results=%+v", results)
			}
			if !exists {
				if !errors.Is(err, ErrSomeFilesFailed) || !errors.Is(results[0].Err, backend.ErrNotFound) {
					t.Fatalf("results=%+v err=%v", results, err)
				}
				return
			}
			data, readErr := os.ReadFile(results[0].File.Destination)
			if err != nil || readErr != nil || string(data) != "single file" {
				t.Fatalf("pull=%v read=%v", err, readErr)
			}
		})
	}
}

func TestPullBacksUpChangedFiles(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "file")
	if err := os.WriteFile(dest, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &mockBackend{files: map[string]string{"file": "remote"}}
	service := &Service{Backend: m}
	cfg := testConfig(t, root, "file")
	for _, step := range []struct {
		content string
		dry     bool
		action  Action
		backups int
	}{
		{"remote", true, ActionUpdated, 0},
		{"original", false, ActionUnchanged, 0},
		{"remote", false, ActionUpdated, 1},
		{"remote", false, ActionUnchanged, 1},
		{"latest", false, ActionUpdated, 2},
	} {
		m.files["file"] = step.content
		results, err := service.Pull(context.Background(), cfg, Options{DryRun: step.dry})
		if err != nil {
			t.Fatal(err)
		}
		if results[0].Action != step.action {
			t.Fatalf("action = %s, want %s", results[0].Action, step.action)
		}
		backups, err := filepath.Glob(dest + ".*.bak*")
		if err != nil || len(backups) != step.backups {
			t.Fatalf("backups = %v, error = %v", backups, err)
		}
		if !step.dry {
			data, err := os.ReadFile(dest)
			if err != nil || string(data) != step.content {
				t.Fatal("destination content mismatch")
			}
		}
	}
	backups, _ := filepath.Glob(dest + ".*.bak*")
	contents := map[string]bool{}
	for _, backup := range backups {
		data, err := os.ReadFile(backup)
		if err != nil {
			t.Fatal(err)
		}
		contents[string(data)] = true
	}
	if !contents["original"] || !contents["remote"] {
		t.Fatal("previous versions were not preserved")
	}
}
