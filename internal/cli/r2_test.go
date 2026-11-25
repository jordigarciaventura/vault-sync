package cli

import (
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func TestR2Registered(t *testing.T) {
	b, err := Registry().New(backend.Config{Type: "r2", Account: "0123456789abcdef0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Name() != "Cloudflare R2" {
		t.Fatal(b.Name())
	}
	if _, ok := b.(backend.FileLister); !ok {
		t.Fatal("missing directory discovery")
	}
	if _, ok := b.(backend.AccountManager); !ok {
		t.Fatal("missing browser login/logout")
	}
	if _, ok := b.(backend.ProjectInitializer); !ok {
		t.Fatal("missing initialization")
	}
}
