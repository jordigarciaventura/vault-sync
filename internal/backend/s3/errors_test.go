package s3

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func TestRequestError(t *testing.T) {
	for _, tc := range []struct {
		code, want string
		notFound   bool
	}{
		{"403", "access denied", false},
		{"AccessDenied", "IAM/bucket permissions", false},
		{"404", "AWS 404", true},
		{"NoSuchKey", "AWS NoSuchKey", true},
		{"BucketAlreadyExists", "globally unique", false},
		{"OptInRequired", "enable the selected region", false},
		{"400", "eu-south-2", false},
		{"InvalidToken", "opt-in region", false},
		{"301", "another region", false},
		{"ExpiredToken", "expired", false},
		{"secret-value", "AWS CLI request failed", false},
		{"UnrecognizedCode", "AWS CLI request failed", false},
	} {
		t.Run(tc.code, func(t *testing.T) {
			err := requestError("An error occurred (" + tc.code + ") when calling the HeadBucket operation: secret-value\nsecret-value")
			if !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret-value") || errors.Is(err, backend.ErrNotFound) != tc.notFound {
				t.Fatalf("error: %v", err)
			}
		})
	}
	if err := requestError("secret-value (403)"); strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("unstructured output leaked: %v", err)
	}
}
func TestUnavailableBucketDoesNotCreate(t *testing.T) {
	for _, code := range []string{"403", "AccessDenied"} {
		b := &Backend{account: "123", region: "eu-south-2", authenticated: true, run: func(_ context.Context, _ []byte, args ...string) ([]byte, error) {
			if args[1] != "head-bucket" {
				t.Fatalf("must not create inaccessible bucket: %v", args)
			}
			return nil, requestError("An error occurred (" + code + ") when calling the HeadBucket operation: hidden")
		}}
		_, err := b.InitializeProject(context.Background(), "vault-sync")
		if !errors.Is(err, backend.ErrContainerUnavailable) || !strings.Contains(err.Error(), "vault-sync") || !strings.Contains(err.Error(), "globally unique") {
			t.Fatalf("error: %v", err)
		}
	}
}
