package backend

import (
	"context"
	"io"
	"strings"
	"testing"
)

type nopBackend struct{ cfg Config }

func (nopBackend) Name() string                       { return "nop" }
func (nopBackend) Authenticate(context.Context) error { return nil }
func (nopBackend) Close() error                       { return nil }
func (nopBackend) ResolveFile(context.Context, string, string) (io.ReadCloser, error) {
	return nil, ErrNotFound
}
func (nopBackend) StoreFile(context.Context, string, string, []byte) error { return nil }

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	r.Register("b", func(cfg Config) (Backend, error) { return nopBackend{cfg}, nil })
	r.Register("a", func(cfg Config) (Backend, error) { return nopBackend{cfg}, nil })

	if got := strings.Join(r.Types(), ","); got != "a,b" {
		t.Errorf("Types = %s", got)
	}
	b, err := r.New(Config{Type: "a", Account: "acc"})
	if err != nil {
		t.Fatal(err)
	}
	if nb := b.(nopBackend); nb.cfg.Account != "acc" || nb.cfg.Logger == nil {
		t.Errorf("config not passed through: %+v", nb.cfg)
	}
	if _, err := r.New(Config{Type: "zzz"}); err == nil || !strings.Contains(err.Error(), "supported: a, b") {
		t.Errorf("err = %v", err)
	}
}

func TestRegistryDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	r := NewRegistry()
	f := func(Config) (Backend, error) { return nopBackend{}, nil }
	r.Register("x", f)
	r.Register("x", f)
}
