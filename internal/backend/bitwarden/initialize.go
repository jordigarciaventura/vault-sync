package bitwarden

import (
	"context"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

var _ backend.ProjectInitializer = (*Backend)(nil)

// SetupFields describes the optional account check used by init.
func (b *Backend) SetupFields() []backend.SetupField {
	return []backend.SetupField{{Key: "server", Label: "Bitwarden server URL", Default: "https://vault.bitwarden.com"}, {Key: "account", Label: "Bitwarden account email (optional)", Default: b.account}}
}

// InitializeProject authenticates and ensures the personal folder exists.
// Prefixes are virtual and need no placeholder items.
func (b *Backend) InitializeProject(ctx context.Context, project string) (backend.ProjectSetup, error) {
	container, _, err := backend.SplitProject(project)
	if err != nil {
		return backend.ProjectSetup{}, err
	}
	if err := b.Authenticate(ctx); err != nil {
		return backend.ProjectSetup{}, err
	}
	if _, _, err := b.folder(ctx, container, true); err != nil {
		return backend.ProjectSetup{}, err
	}
	st, err := b.client.status(ctx)
	if err != nil {
		return backend.ProjectSetup{}, err
	}
	server := st.ServerURL
	if server == "" {
		server = b.server
	}
	return backend.ProjectSetup{Project: project, Config: backend.Config{Type: Type, Account: b.account, Server: server}}, nil
}
