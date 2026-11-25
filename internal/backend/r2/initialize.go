package r2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

var _ backend.ProjectInitializer = (*Backend)(nil)

// SetupFields collects the non-secret endpoint account ID.
func (b *Backend) SetupFields() []backend.SetupField {
	return []backend.SetupField{{Key: "account", Label: "Cloudflare account ID (required, 32 hexadecimal characters)", Default: b.account}}
}

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// InitializeProject creates a private bucket or reuses an accessible bucket.
// Public access settings on existing buckets are never changed.
func (b *Backend) InitializeProject(ctx context.Context, project string) (backend.ProjectSetup, error) {
	bucket, _, err := backend.SplitProject(project)
	if err != nil {
		return backend.ProjectSetup{}, err
	}
	if !bucketPattern.MatchString(bucket) {
		return backend.ProjectSetup{}, errors.New("invalid R2 bucket name; use 3–63 lowercase letters, digits or hyphens, starting and ending with a letter or digit")
	}
	if err := b.Authenticate(ctx); err != nil {
		return backend.ProjectSetup{}, err
	}
	if response, err := b.request(ctx, http.MethodGet, b.bucketPath(bucket), nil, ""); err != nil {
		if !errors.Is(err, backend.ErrNotFound) {
			return backend.ProjectSetup{}, err
		}
		body, _ := json.Marshal(struct {
			Name string `json:"name"`
		}{bucket})
		response, err := b.request(ctx, http.MethodPost, "/accounts/"+b.account+"/r2/buckets", body, "application/json")
		if err != nil {
			return backend.ProjectSetup{}, fmt.Errorf("create R2 bucket: %w", err)
		}
		defer response.Body.Close()
		if err := decodeResponse(response, nil); err != nil {
			return backend.ProjectSetup{}, err
		}
	} else {
		defer response.Body.Close()
		if err := decodeResponse(response, nil); err != nil {
			return backend.ProjectSetup{}, err
		}
	}
	return backend.ProjectSetup{Project: project, Config: backend.Config{Type: Type, Account: b.account}}, nil
}
