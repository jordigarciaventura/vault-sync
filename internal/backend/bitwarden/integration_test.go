//go:build integration

// Integration tests against a real Bitwarden vault. They only run with
//
//	go test -tags integration ./internal/backend/bitwarden/
//
// and require an unlocked, dedicated TEST vault:
//
//	export BW_SESSION=$(bw unlock --raw)
//	export VAULT_SYNC_IT_PROJECT=vault-sync-integration   # folder to use
//
// The tests create and delete items inside that folder only.
package bitwarden

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func integrationBackend(t *testing.T) (*Backend, string) {
	t.Helper()
	project := os.Getenv("VAULT_SYNC_IT_PROJECT")
	if project == "" || os.Getenv(sessionEnv) == "" {
		t.Skip("set BW_SESSION and VAULT_SYNC_IT_PROJECT to run Bitwarden integration tests")
	}
	b, err := New(backend.Config{Type: Type})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b.(*Backend), project
}

func randomName(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return "it/" + hex.EncodeToString(buf) + ".env"
}

func cleanup(t *testing.T, b *Backend, project, name string) {
	t.Cleanup(func() {
		ctx := context.Background()
		f, ok, err := b.folder(ctx, project, false)
		if err != nil || !ok {
			return
		}
		delete(b.items, f.ID)
		items, _ := b.listItems(ctx, f.ID)
		for _, it := range items {
			if it.Name == name {
				_, _ = b.client.run.Run(ctx, b.client.env(), nil, "delete", "item", it.ID)
			}
		}
	})
}

func roundTrip(t *testing.T, content []byte) {
	b, project := integrationBackend(t)
	name := randomName(t)
	cleanup(t, b, project, name)
	ctx := context.Background()

	if err := b.StoreFile(ctx, project, name, content); err != nil {
		if errors.Is(err, ErrPremiumRequired) {
			t.Skip("test account has no Bitwarden Premium; attachments unavailable")
		}
		t.Fatalf("StoreFile: %v", err)
	}
	rc, err := b.ResolveFile(ctx, project, name)
	if err != nil {
		t.Fatalf("ResolveFile: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(content))
	}
}

func TestIntegrationSecureNote(t *testing.T) { roundTrip(t, []byte("A=1\nB=2\n")) }

func TestIntegrationAttachment(t *testing.T) {
	roundTrip(t, []byte(strings.Repeat("x", maxNoteSize+100)))
}
