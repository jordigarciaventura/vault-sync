package backend

import (
	"context"
	"errors"
)

// ErrContainerUnavailable allows interactive init to request another container name.
// It must only be returned before creation, or when creation definitely failed.
var ErrContainerUnavailable = errors.New("container unavailable")

// SetupField describes a non-secret backend setting collected by init.
// Key names a Config field: account, environment, server or region.
type SetupField struct {
	Key     string
	Label   string
	Default string
}

// ProjectSetup contains the portable location of an initialized container.
// Config must contain only non-secret settings; credentials stay with the provider.
type ProjectSetup struct {
	Project string
	Config  Config
}

// ProjectInitializer supports authenticated, empty container creation by init.
// Existing containers may be reused; files must never be uploaded or replaced.
type ProjectInitializer interface {
	SetupFields() []SetupField
	InitializeProject(ctx context.Context, project string) (ProjectSetup, error)
}
