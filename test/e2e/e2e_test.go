//go:build e2e

// Package e2e runs the vault-sync binary end to end against real provider
// accounts. The tests are excluded from the normal suite and only run with
//
//	go test -tags e2e ./test/e2e/
//
// Every backend runs by default. To run only some, list them in
// VAULT_SYNC_E2E (comma-separated, e.g. "bitwarden"). Use a DEDICATED test
// account: every run overwrites .env in the test project (vault-sync-e2e by
// default, created on first push).
//
// Authentication is left to the provider CLIs and must work without a
// terminal:
//
//	export BW_SESSION=$(bw unlock --raw)   # Bitwarden (after `bw login`)
//	infisical login                        # Infisical, or export INFISICAL_TOKEN
//
// Optional overrides (per backend, NAME = BITWARDEN or INFISICAL):
//
//	VAULT_SYNC_E2E_<NAME>_PROJECT   project / folder to use
//	VAULT_SYNC_E2E_<NAME>_ACCOUNT   account the session must belong to
//	VAULT_SYNC_E2E_INFISICAL_SERVER Infisical instance (default app.infisical.com)
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// binary is the vault-sync executable built once by TestMain.
var binary string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "vault-sync-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	binary = filepath.Join(dir, "vault-sync")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "github.com/jordigarciaventura/vault-sync/cmd/vault-sync")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "build vault-sync: %v\n", err)
		return 1
	}
	return m.Run()
}

func TestBitwarden(t *testing.T) {
	requireBackend(t, "bitwarden")
	if os.Getenv("BW_SESSION") == "" {
		t.Fatal("BW_SESSION is empty; run `export BW_SESSION=$(bw unlock --raw)` first, or set VAULT_SYNC_E2E to the backends you want to test")
	}
	roundTrip(t, "bitwarden", nil)
}

func TestInfisical(t *testing.T) {
	requireBackend(t, "infisical")
	roundTrip(t, "infisical", map[string]string{"server": os.Getenv("VAULT_SYNC_E2E_INFISICAL_SERVER")})
}

// backends lists every backend with an end-to-end test.
var backends = []string{"bitwarden", "infisical"}

// requireBackend skips the test when VAULT_SYNC_E2E is set and does not list
// name. An unset or empty VAULT_SYNC_E2E runs every backend.
func requireBackend(t *testing.T, name string) {
	t.Helper()
	var selected []string
	for _, s := range strings.Split(os.Getenv("VAULT_SYNC_E2E"), ",") {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if !slices.Contains(backends, s) {
			t.Fatalf("VAULT_SYNC_E2E lists unknown backend %q; use a comma-separated subset of %s", s, strings.Join(backends, ","))
		}
		selected = append(selected, s)
	}
	if len(selected) > 0 && !slices.Contains(selected, name) {
		t.Skipf("%s is not listed in VAULT_SYNC_E2E", name)
	}
}

// roundTrip pushes a random .env, deletes it locally and pulls it back,
// checking every step of the pull lifecycle on the way.
func roundTrip(t *testing.T, name string, backendOverrides map[string]string) {
	dir := t.TempDir()
	writeManifest(t, name, dir, backendOverrides)
	envFile := filepath.Join(dir, ".env")

	// Infisical trims surrounding whitespace except one final newline, so keep
	// the content to plain KEY=value lines.
	content := []byte(fmt.Sprintf("E2E_TOKEN=%s\nE2E_RUN=%s\n", randomHex(t), time.Now().UTC().Format(time.RFC3339)))
	if err := os.WriteFile(envFile, content, 0o600); err != nil {
		t.Fatal(err)
	}

	// Each step depends on the previous one, so stop at the first failure.
	steps := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"push", func(t *testing.T) {
			out := mustRun(t, dir, "push")
			expect(t, out, "Successfully pushed 1 file")
			if err := os.Remove(envFile); err != nil {
				t.Fatal(err)
			}
		}},
		{"pull dry run", func(t *testing.T) {
			out := mustRun(t, dir, "pull", "--dry-run")
			expect(t, out, ".env (would create)")
			if _, err := os.Stat(envFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("dry run created %s (stat err: %v)", envFile, err)
			}
		}},
		{"pull creates", func(t *testing.T) {
			out := mustRun(t, dir, "pull")
			expect(t, out, ".env (created)")
			assertFile(t, envFile, content)
		}},
		{"pull unchanged", func(t *testing.T) {
			out := mustRun(t, dir, "pull")
			expect(t, out, ".env (unchanged)")
		}},
		{"pull overwrites local changes", func(t *testing.T) {
			if err := os.WriteFile(envFile, []byte("LOCAL=edit\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			out := mustRun(t, dir, "pull")
			expect(t, out, ".env (updated)")
			assertFile(t, envFile, content)
		}},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.fn) {
			return
		}
	}
}

// writeManifest copies testdata/<name>.yaml to dir/secrets.yaml, applying the
// VAULT_SYNC_E2E_<NAME>_* overrides.
func writeManifest(t *testing.T, name, dir string, backendOverrides map[string]string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse testdata/%s.yaml: %v", name, err)
	}
	b, ok := m["backend"].(map[string]any)
	if !ok {
		t.Fatalf("testdata/%s.yaml has no backend section", name)
	}

	prefix := "VAULT_SYNC_E2E_" + strings.ToUpper(name) + "_"
	if v := os.Getenv(prefix + "PROJECT"); v != "" {
		m["project"] = v
	}
	if v := os.Getenv(prefix + "ACCOUNT"); v != "" {
		b["account"] = v
	}
	for k, v := range backendOverrides {
		if v != "" {
			b[k] = v
		}
	}

	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.yaml"), out, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("project %v", m["project"])
}

// mustRun runs vault-sync in dir and fails the test on a non-zero exit. Stdin
// is not a terminal, so missing credentials fail instead of prompting.
func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), "VAULT_SYNC_NO_UPDATE_CHECK=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// vault-sync never prints secret values, so its output is safe to log.
		t.Fatalf("vault-sync %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func expect(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Fatalf("output does not contain %q:\n%s", want, out)
	}
}

// assertFile checks contents and permissions without printing the contents.
func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: pulled %d bytes that differ from the %d bytes pushed", path, len(got), len(want))
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("%s: permissions %04o, want 0600", path, perm)
	}
}

func randomHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(buf)
}
