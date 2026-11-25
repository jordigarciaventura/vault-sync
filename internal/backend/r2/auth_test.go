package r2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func TestBrowserLogin(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		value, _ := New(backend.Config{Account: testAccount})
		b := value.(*Backend)
		b.getenv = func(string) string { return "" }
		b.interactive = func() bool { return interactive }
		var calls []string
		loggedIn := false
		b.run = func(_ context.Context, terminal bool, args ...string) ([]byte, error) {
			calls = append(calls, args[0])
			if args[0] == "login" {
				if !terminal {
					t.Error("login requires interactive runner")
				}
				loggedIn = true
				return nil, nil
			}
			if !reflect.DeepEqual(args, []string{"auth", "token", "--json"}) || terminal {
				t.Error("incorrect token request")
			}
			if !loggedIn {
				return nil, backend.ErrAuthentication
			}
			return []byte(`{"type":"oauth","token":"oauth-token"}`), nil
		}
		err := b.Authenticate(context.Background())
		if interactive {
			if err != nil || !reflect.DeepEqual(calls, []string{"auth", "login", "auth"}) {
				t.Fatalf("login: %v %v", calls, err)
			}
			if err := b.Authenticate(context.Background()); err != nil || len(calls) != 3 {
				t.Fatal("session not cached")
			}
			b.Close()
			if b.token != "" {
				t.Fatal("close retained token")
			}
		} else if !errors.Is(err, backend.ErrAuthentication) || len(calls) != 1 {
			t.Fatalf("noninteractive: %v %v", calls, err)
		}
	}
}

func TestEnvironmentAuthentication(t *testing.T) {
	b := testBackend(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"success":true}`) })
	b.run = func(context.Context, bool, ...string) ([]byte, error) {
		t.Fatal("environment auth invoked Wrangler")
		return nil, nil
	}
	if _, err := b.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.Logout(context.Background()); err == nil {
		t.Fatal("environment logout must explain unsetting token")
	}
}

func TestOAuthRefresh(t *testing.T) {
	calls := 0
	b := testBackend(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "Bearer expired" {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") != "Bearer refreshed" {
			t.Error("unexpected token")
		}
		_, _ = io.WriteString(w, `{"success":true}`)
	})
	b.getenv = func(string) string { return "" }
	b.token = "expired"
	b.run = func(context.Context, bool, ...string) ([]byte, error) {
		return []byte(`{"type":"oauth","token":"refreshed"}`), nil
	}
	if err := b.StoreFile(context.Background(), "bucket", "file", nil); err != nil || calls != 2 {
		t.Fatalf("refresh: %v, calls %d", err, calls)
	}
}

func TestStatusNeverLogsIn(t *testing.T) {
	value, _ := New(backend.Config{Account: testAccount})
	b := value.(*Backend)
	b.getenv = func(string) string { return "" }
	b.interactive = func() bool { return true }
	b.run = func(_ context.Context, terminal bool, args ...string) ([]byte, error) {
		if terminal || args[0] != "auth" {
			t.Fatal("status started login")
		}
		return nil, backend.ErrAuthentication
	}
	status, err := b.Status(context.Background())
	if err != nil || status.LoggedIn {
		t.Fatalf("status: %+v %v", status, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Authenticate(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

func TestLoginBeforeManifest(t *testing.T) {
	value, _ := New(backend.Config{})
	b := value.(*Backend)
	b.getenv = func(string) string { return "" }
	b.interactive = func() bool { return false }
	b.run = func(_ context.Context, terminal bool, args ...string) ([]byte, error) {
		if terminal || args[0] != "auth" {
			t.Fatal("unexpected command")
		}
		return []byte(`{"type":"oauth","token":"existing-session"}`), nil
	}
	if status, err := b.Login(context.Background()); err != nil || !status.LoggedIn {
		t.Fatalf("login: %+v %v", status, err)
	}
}

func TestInvalidTokenOutput(t *testing.T) {
	for _, out := range []string{"SECRET", `{"type":"oauth","token":""}`, `{"type":"api_key","key":"SECRET"}`, `{"type":"oauth","token":"SECRET\n"}`} {
		value, _ := New(backend.Config{Account: testAccount, NonInteractive: true})
		b := value.(*Backend)
		b.getenv = func(string) string { return "" }
		b.run = func(context.Context, bool, ...string) ([]byte, error) { return []byte(out), nil }
		if err := b.Authenticate(context.Background()); err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("invalid token error: %v", err)
		}
	}
}

func TestRevokedSessionBrowserLogin(t *testing.T) {
	loggedIn := false
	b := testBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if !loggedIn {
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, `{"success":true}`)
	})
	b.getenv = func(string) string { return "" }
	b.interactive = func() bool { return true }
	b.token = "revoked"
	b.run = func(_ context.Context, terminal bool, args ...string) ([]byte, error) {
		if args[0] == "login" {
			if !terminal {
				t.Error("expected browser login")
			}
			loggedIn = true
			return nil, nil
		}
		return []byte(`{"type":"oauth","token":"session"}`), nil
	}
	if err := b.StoreFile(context.Background(), "bucket", "file", nil); err != nil || !loggedIn {
		t.Fatalf("relogin: %v", err)
	}
}
