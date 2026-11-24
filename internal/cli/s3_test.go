package cli

import (
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func TestS3Registered(t *testing.T) {
	b, err := Registry().New(backend.Config{Type: "s3", Account: "123456789012"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Name() != "Amazon S3" {
		t.Fatalf("name: %s", b.Name())
	}
	if _, ok := b.(backend.FileLister); !ok {
		t.Fatal("S3 must support directory discovery")
	}
}
