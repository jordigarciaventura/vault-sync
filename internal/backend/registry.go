package backend

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
)

// Registry maps backend type identifiers to their factories.
type Registry struct {
	factories map[string]Factory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Register adds a factory. It panics on duplicate or empty names because that
// is always a programming error.
func (r *Registry) Register(typ string, f Factory) {
	if typ == "" || f == nil {
		panic("backend: Register requires a type and a factory")
	}
	if _, dup := r.factories[typ]; dup {
		panic("backend: duplicate registration for " + typ)
	}
	r.factories[typ] = f
}

// Types returns the registered type identifiers in sorted order.
func (r *Registry) Types() []string {
	return slices.Sorted(maps.Keys(r.factories))
}

// New constructs the backend registered under cfg.Type.
func (r *Registry) New(cfg Config) (Backend, error) {
	f, ok := r.factories[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown backend type %q (supported: %s)", cfg.Type, strings.Join(r.Types(), ", "))
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return f(cfg)
}
