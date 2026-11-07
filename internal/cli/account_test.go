package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

// accountBackend is a memBackend with a simulated login state.
type accountBackend struct {
	memBackend
	st      backend.AccountStatus
	logins  int
	logouts int
}

func (a *accountBackend) Status(context.Context) (backend.AccountStatus, error) { return a.st, nil }
func (a *accountBackend) Login(context.Context) (backend.AccountStatus, error) {
	a.logins++
	a.st = backend.AccountStatus{LoggedIn: true, User: "me@example.com", Server: "https://vault.example"}
	return a.st, nil
}
func (a *accountBackend) Logout(context.Context) error {
	a.logouts++
	a.st = backend.AccountStatus{}
	return nil
}

func runAccount(t *testing.T, b *accountBackend, args ...string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	manifest := writeManifest(t, dir)
	return runWith(t, func(backend.Config) (backend.Backend, error) { return b, nil }, append(args, "--config", manifest)...)
}

func TestLoginLogsIn(t *testing.T) {
	b := &accountBackend{}
	out, err := runAccount(t, b, "login")
	if err != nil {
		t.Fatal(err)
	}
	if b.logins != 1 || !strings.Contains(out, "Logged in to Memory") || !strings.Contains(out, "Account: me@example.com") {
		t.Errorf("logins = %d, out = %q", b.logins, out)
	}
	if strings.Contains(out, "Vault:") {
		t.Errorf("vault state shown right after login: %q", out)
	}
}

func TestLoginShowsCurrentAccount(t *testing.T) {
	b := &accountBackend{st: backend.AccountStatus{LoggedIn: true, Locked: true, User: "me@example.com", AccountMismatch: true}}
	out, err := runAccount(t, b, "login")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Already logged in to Memory", "Account: me@example.com", "Vault:   locked", "Warning:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if b.logins != 0 {
		t.Error("should not log in again")
	}
}

func TestLoginSwitchesServer(t *testing.T) {
	b := &accountBackend{st: backend.AccountStatus{LoggedIn: true, User: "me@example.com", Server: "https://other.example", ServerMismatch: true}}
	out, err := runAccount(t, b, "login")
	if err != nil {
		t.Fatal(err)
	}
	if b.logins != 1 || !strings.Contains(out, "Logged in to Memory") || !strings.Contains(out, "Server:  https://vault.example") || strings.Contains(out, "Warning:") {
		t.Errorf("logins = %d, out = %q", b.logins, out)
	}
}

func TestLogout(t *testing.T) {
	b := &accountBackend{st: backend.AccountStatus{LoggedIn: true, User: "me@example.com"}}
	out, err := runAccount(t, b, "logout")
	if err != nil || b.logouts != 1 || !strings.Contains(out, "Logged out of Memory (me@example.com)") {
		t.Errorf("logouts = %d, out = %q, err = %v", b.logouts, out, err)
	}
	out, err = runAccount(t, b, "logout")
	if err != nil || b.logouts != 1 || !strings.Contains(out, "Not logged in") {
		t.Errorf("logouts = %d, out = %q, err = %v", b.logouts, out, err)
	}
}

func TestLoginUnsupportedBackend(t *testing.T) {
	dir := t.TempDir()
	manifest := writeManifest(t, dir)
	_, err := run(t, &memBackend{}, "login", "--config", manifest)
	if err == nil || !strings.Contains(err.Error(), "does not support login") {
		t.Errorf("err = %v", err)
	}
}

func TestLoginWithoutManifestUsesOnlyBackend(t *testing.T) {
	t.Chdir(t.TempDir())
	b := &accountBackend{st: backend.AccountStatus{LoggedIn: true, User: "me@example.com"}}
	out, err := runWith(t, func(backend.Config) (backend.Backend, error) { return b, nil }, "login")
	if err != nil || !strings.Contains(out, "Already logged in") {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

func runWithTwoBackends(t *testing.T, args ...string) (string, *backend.Config, error) {
	t.Helper()
	var got backend.Config
	f := func(cfg backend.Config) (backend.Backend, error) {
		got = cfg
		return &accountBackend{st: backend.AccountStatus{LoggedIn: true, User: "me@example.com"}}, nil
	}
	r := backend.NewRegistry()
	r.Register("memory", f)
	r.Register("other", f)
	var out bytes.Buffer
	a := &app{registry: r, stdout: &out, stderr: &out}
	root := newRootCommand(a)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), &got, err
}

func TestLoginBackendFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, _, err := runWithTwoBackends(t, "login"); err == nil || !strings.Contains(err.Error(), "--backend (memory, other)") {
		t.Errorf("without flag: err = %v", err)
	}
	out, cfg, err := runWithTwoBackends(t, "login", "--backend", "other")
	if err != nil || cfg.Type != "other" || !strings.Contains(out, "Already logged in") {
		t.Errorf("out = %q, cfg = %+v, err = %v", out, cfg, err)
	}
	if _, _, err := runWithTwoBackends(t, "logout", "-b", "nope"); err == nil || !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("unknown: err = %v", err)
	}

	manifest := writeManifest(t, t.TempDir())
	if _, _, err := runWithTwoBackends(t, "login", "--config", manifest, "--backend", "other"); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Errorf("conflict: err = %v", err)
	}
	if _, cfg, err := runWithTwoBackends(t, "login", "--config", manifest, "--backend", "memory"); err != nil || cfg.Type != "memory" {
		t.Errorf("matching flag: cfg = %+v, err = %v", cfg, err)
	}
}
