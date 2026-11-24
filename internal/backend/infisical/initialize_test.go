package infisical

import (
	"context"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func TestInitializeEmptyProject(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.api.noExample = true
	setup, err := h.b.InitializeProject(context.Background(), "new-project/nested")
	if err != nil {
		t.Fatal(err)
	}
	if setup.Project != projectUUID+"/nested" || setup.Config.Environment != "dev" || setup.Config.Server != h.srv.URL {
		t.Fatalf("setup: %+v", setup)
	}
	h.api.mu.Lock()
	defer h.api.mu.Unlock()
	if len(h.api.created) != 1 || len(h.api.secrets) != 0 || !h.api.folders["dev\x00/nested"] {
		t.Fatal("expected one empty project with prefix folder")
	}
}
func TestInitializeExistingProject(t *testing.T) {
	h := newHarness(t, backend.Config{Environment: "prod"})
	setup, err := h.b.InitializeProject(context.Background(), projectSlug)
	if err != nil {
		t.Fatal(err)
	}
	if setup.Project != projectUUID || setup.Config.Environment != "prod" {
		t.Fatalf("setup: %+v", setup)
	}
	h.api.mu.Lock()
	defer h.api.mu.Unlock()
	if len(h.api.created) != 0 || len(h.api.secrets) != 0 {
		t.Fatal("existing project mutated")
	}
}
