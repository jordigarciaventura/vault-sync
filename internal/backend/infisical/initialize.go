package infisical

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

var _ backend.ProjectInitializer = (*Backend)(nil)

// SetupFields describes portable settings collected before Infisical login.
func (b *Backend) SetupFields() []backend.SetupField {
	return []backend.SetupField{
		{Key: "server", Label: "Infisical server URL", Default: defaultDomain},
		{Key: "environment", Label: "Infisical environment", Default: DefaultEnvironment},
		{Key: "account", Label: "Infisical account (optional)", Default: b.account},
	}
}

// InitializeProject logs in, creates or resolves the project and preserves the supplied reference.
func (b *Backend) InitializeProject(ctx context.Context, ref string) (backend.ProjectSetup, error) {
	container, prefix, err := backend.SplitProject(ref)
	if err != nil {
		return backend.ProjectSetup{}, err
	}
	if prefix != "" {
		if _, err := locate(prefix + "/placeholder"); err != nil {
			return backend.ProjectSetup{}, err
		}
	}
	if err := b.Authenticate(ctx); err != nil {
		return backend.ProjectSetup{}, err
	}
	id, err := b.projectID(ctx, container)
	if np := (*noProjectError)(nil); errors.As(err, &np) {
		id, err = b.createProject(ctx, container)
	}
	if err != nil {
		return backend.ProjectSetup{}, err
	}
	if prefix != "" {
		if err := b.api.createFolder(ctx, id, b.env, "/"+prefix); err != nil {
			return backend.ProjectSetup{}, fmt.Errorf("initialize Infisical folder: %w", err)
		}
	}
	server := b.server
	if server == "" {
		server = strings.TrimSuffix(b.api.base, "/api")
	}
	return backend.ProjectSetup{Project: ref, Config: backend.Config{Type: Type, Account: b.account, Environment: b.env, Server: server}}, nil
}
