package r2

import (
	"errors"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func TestRequestError(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{{404, backend.ErrNotFound}, {401, backend.ErrAuthentication}, {409, backend.ErrContainerUnavailable}, {403, nil}, {500, nil}, {413, nil}} {
		err := requestError(tc.status)
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Fatalf("%d: %v", tc.status, err)
		}
	}
}
