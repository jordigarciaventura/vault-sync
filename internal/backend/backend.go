// Package backend defines the provider-independent interface every secrets
// backend implements, plus the registry used to construct them by name.
//
// Nothing in this package (or in the packages that consume it) may depend on a
// concrete provider.
package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
)

// Backend is a secrets provider able to store and retrieve files identified by
// a logical filename inside a project namespace.
type Backend interface {
	// Name is the human-readable provider name, e.g. "Bitwarden".
	Name() string

	// Authenticate makes sure the backend is ready to serve requests, using the
	// provider's native authentication mechanism.
	Authenticate(ctx context.Context) error

	// ResolveFile locates the resource called filename in project and returns
	// its contents. Implementations must return an error wrapping ErrNotFound
	// or ErrAmbiguous when appropriate and must never pick an arbitrary match.
	//
	// Implementations may defer the actual download until the first Read, so
	// a caller that only needs to know whether a file exists can Close the
	// reader right away.
	ResolveFile(ctx context.Context, project, filename string) (io.ReadCloser, error)

	// StoreFile creates or replaces the resource called filename in project.
	StoreFile(ctx context.Context, project, filename string, content []byte) error

	// Close releases any resources held by the backend.
	Close() error
}

// Config is the provider-specific configuration handed to a Factory.
type Config struct {
	Type    string
	Account string
	// Environment selects a provider environment (e.g. "dev" or "prod").
	// Backends without environments must reject a non-empty value.
	Environment string
	// Server is the provider instance to use (e.g. a self-hosted URL).
	// Empty means the provider's default. Backends that cannot select a
	// server must reject a non-empty value.
	Server string

	// Logger receives diagnostic messages. It must never be given secret
	// values. Never nil when passed to a Factory.
	Logger *slog.Logger
}

// Factory constructs a backend from its configuration.
type Factory func(cfg Config) (Backend, error)

// Sentinel errors shared by all backends.
var (
	ErrNotFound       = errors.New("resource not found")
	ErrAmbiguous      = errors.New("resource is ambiguous")
	ErrAuthentication = errors.New("authentication failed")
)

// NotFoundError reports that no resource matches a logical filename.
func NotFoundError(project, filename, detail string) error {
	msg := fmt.Sprintf("%q not found in project %q", filename, project)
	if detail != "" {
		msg += ": " + detail
	}
	return fmt.Errorf("%w: %s", ErrNotFound, msg)
}

// AmbiguousError reports that several resources match a logical filename.
func AmbiguousError(project, filename string, matches []string) error {
	return fmt.Errorf("%w: %d resources named %q in project %q (%v); remove or rename the duplicates",
		ErrAmbiguous, len(matches), filename, project, matches)
}

// AccountStatus describes the login state of a backend. It never holds
// credentials or session keys.
type AccountStatus struct {
	// LoggedIn reports whether a user is logged in.
	LoggedIn bool
	// Locked reports whether the logged-in user still has to unlock the
	// vault before secrets can be read.
	Locked bool
	// User identifies the logged-in user (e.g. an email address).
	User string
	// Server is the provider endpoint the user is logged in to, if known.
	Server string
	// AccountMismatch reports that User differs from the configured account.
	AccountMismatch bool
	// ServerMismatch reports that Server differs from the configured server.
	// Login then logs in to the configured server.
	ServerMismatch bool
}

// AccountManager is implemented by backends whose authentication involves an
// explicit login step that vault-sync can drive on the user's behalf.
type AccountManager interface {
	// Status reports the current login state without prompting.
	Status(ctx context.Context) (AccountStatus, error)
	// Login logs in interactively and returns the resulting state. It does
	// nothing when a session to the configured server is active. It must
	// fail with an error wrapping ErrAuthentication when no terminal is
	// available.
	Login(ctx context.Context) (AccountStatus, error)
	// Logout ends the provider session.
	Logout(ctx context.Context) error
}
