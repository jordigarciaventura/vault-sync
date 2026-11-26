package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverEnvFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".env", ".env.local", "app/.env.production", "app/other.txt", ".git/.env", "node_modules/pkg/.env", "vendor/.env", ".venv/.env", "dist/.env"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o000); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, ".env"), filepath.Join(root, ".env.link")); err != nil {
		t.Fatal(err)
	}
	got, err := discoverEnvFiles(context.Background(), root)
	want := []string{".env", ".env.local", "app/.env.production"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := discoverEnvFiles(ctx, root); err == nil {
		t.Fatal("ignored cancellation")
	}
	if got, err := discoverEnvFiles(context.Background(), filepath.Join(root, "missing")); err != nil || len(got) != 0 {
		t.Fatalf("missing directory: %v, %v", got, err)
	}
}
