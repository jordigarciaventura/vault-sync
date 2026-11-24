package bitwarden

import (
	"context"
	"errors"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func TestInitializeEmptyFolder(t *testing.T) {
	v := newFakeVault()
	b := newTestBackend(t, v, "")
	for range 2 {
		result, err := b.InitializeProject(context.Background(), "example/prefix")
		if err != nil || result.Project != "example/prefix" {
			t.Fatalf("result: %+v, %v", result, err)
		}
	}
	if len(v.folders) != 1 || v.folders[0].Name != "example" || len(v.items) != 0 {
		t.Fatal("did not create exactly one empty folder")
	}
}
func TestInitializeAmbiguousFolder(t *testing.T) {
	v := newFakeVault()
	v.addFolder("example")
	v.addFolder("example")
	b := newTestBackend(t, v, "")
	if _, err := b.InitializeProject(context.Background(), "example"); !errors.Is(err, backend.ErrAmbiguous) {
		t.Fatalf("error: %v", err)
	}
	if len(v.folders) != 2 {
		t.Fatal("created duplicate folder")
	}
}

func TestSavedServerMismatchPreventsAccess(t *testing.T) {
	v := newFakeVault()
	b := newTestBackend(t, v, "")
	b.server = "https://vault.example.com"
	if err := b.Authenticate(context.Background()); !errors.Is(err, backend.ErrAuthentication) {
		t.Fatalf("error: %v", err)
	}
	if len(v.folders) != 0 || len(v.items) != 0 {
		t.Fatal("server mismatch changed remote data")
	}
	for _, call := range v.calls {
		if len(call) == 0 || call[0] != "status" {
			t.Fatalf("server mismatch executed %v", call)
		}
	}
	st, err := b.Status(context.Background())
	if err != nil || !st.ServerMismatch {
		t.Fatalf("status %+v, %v", st, err)
	}
	if _, err := b.Login(context.Background()); !errors.Is(err, backend.ErrAuthentication) {
		t.Fatalf("login error: %v", err)
	}
}
func TestNewAcceptsSavedServerWithoutCLI(t *testing.T) {
	t.Setenv(BinaryEnv, "/missing/bw")
	b, err := New(backend.Config{Server: "https://vault.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
}
