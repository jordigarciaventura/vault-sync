package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/config"
)

type initializingBackend struct {
	memBackend
	cfg       backend.Config
	provision func(context.Context, string) (backend.ProjectSetup, error)
}

func (b *initializingBackend) SetupFields() []backend.SetupField {
	return []backend.SetupField{{Key: "region", Label: "Bucket region", Default: "us-east-1"}}
}
func (b *initializingBackend) InitializeProject(ctx context.Context, project string) (backend.ProjectSetup, error) {
	return b.provision(ctx, project)
}

func TestInitProvisionsPortableLocation(t *testing.T) {
	for _, interactive := range []bool{true, false} {
		t.Run(map[bool]string{true: "interactive", false: "flags"}[interactive], func(t *testing.T) {
			t.Chdir(t.TempDir())
			called := 0
			factory := func(cfg backend.Config) (backend.Backend, error) {
				return &initializingBackend{cfg: cfg, provision: func(_ context.Context, project string) (backend.ProjectSetup, error) {
					called++
					if project != "bucket/prefix" || cfg.Region != "eu-west-1" || cfg.NonInteractive == interactive {
						t.Fatalf("project=%s config=%+v", project, cfg)
					}
					return backend.ProjectSetup{Project: "resolved-id/prefix", Config: backend.Config{Type: "memory", Region: cfg.Region, Account: "123"}}, nil
				}}, nil
			}
			args := []string{"init", "--non-interactive", "--backend", "memory", "--project", "bucket/prefix", "--region", "eu-west-1", "--file", "config/"}
			input := ""
			if interactive {
				args = []string{"init"}
				input = "\nmemory\neu-west-1\nbucket/prefix\nconfig/\n\n"
			}
			if out, err := runInput(t, factory, input, args...); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			data, err := os.ReadFile("secrets.yaml")
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Parse(data, mustWorkingDir(t), config.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runWith(t, func(loaded backend.Config) (backend.Backend, error) {
				if loaded.Region != "eu-west-1" || loaded.Account != "123" {
					t.Fatalf("saved backend settings were not propagated: %+v", loaded)
				}
				return &memBackend{}, nil
			}, "validate", "--config", "secrets.yaml"); err != nil {
				t.Fatal(err)
			}
			if called != 1 || cfg.Project != "resolved-id/prefix" || cfg.Backend.Region != "eu-west-1" || cfg.Backend.Account != "123" {
				t.Fatalf("calls %d, manifest %s", called, data)
			}
		})
	}
}

func mustWorkingDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInitProvisionFailurePreservesManifest(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("secrets.yaml", []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	factory := func(backend.Config) (backend.Backend, error) {
		return &initializingBackend{provision: func(context.Context, string) (backend.ProjectSetup, error) {
			calls++
			return backend.ProjectSetup{}, errors.New("login failed")
		}}, nil
	}
	args := []string{"init", "--non-interactive", "--backend", "memory", "--project", "bucket", "--file", "x", "--force"}
	if _, err := runWith(t, factory, args...); err == nil || !strings.Contains(err.Error(), "login failed") {
		t.Fatalf("error: %v", err)
	}
	data, _ := os.ReadFile("secrets.yaml")
	if string(data) != "original" || calls != 1 {
		t.Fatal("failed initialization changed manifest")
	}
	calls = 0
	if _, err := runWith(t, factory, "init", "--non-interactive", "--backend", "memory", "--project", "bucket", "--file", "../escape", "--force"); err == nil {
		t.Fatal("accepted invalid file")
	}
	if calls != 0 {
		t.Fatal("invalid manifest provisioned a container")
	}
}

func TestInteractiveInitRetriesUnavailableContainer(t *testing.T) {
	t.Chdir(t.TempDir())
	var names []string
	factory := func(cfg backend.Config) (backend.Backend, error) {
		return &initializingBackend{provision: func(_ context.Context, name string) (backend.ProjectSetup, error) {
			names = append(names, name)
			if len(names) == 1 {
				return backend.ProjectSetup{}, fmt.Errorf("%w: choose a unique bucket name", backend.ErrContainerUnavailable)
			}
			return backend.ProjectSetup{Project: name, Config: cfg}, nil
		}}, nil
	}
	out, err := runInput(t, factory, "\nmemory\n\nvault-sync\n.env\n\nmy-unique-bucket\n", "init")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(names) != 2 || names[1] != "my-unique-bucket" || !strings.Contains(out, "Another container name") {
		t.Fatalf("names %v, output %s", names, out)
	}
	data, _ := os.ReadFile("secrets.yaml")
	if !strings.Contains(string(data), "project: my-unique-bucket") {
		t.Fatalf("manifest: %s", data)
	}
}
func TestNonInteractiveInitDoesNotRetryUnavailableContainer(t *testing.T) {
	t.Chdir(t.TempDir())
	calls := 0
	factory := func(backend.Config) (backend.Backend, error) {
		return &initializingBackend{provision: func(context.Context, string) (backend.ProjectSetup, error) {
			calls++
			return backend.ProjectSetup{}, backend.ErrContainerUnavailable
		}}, nil
	}
	_, err := runWith(t, factory, "init", "--non-interactive", "--backend", "memory", "--project", "taken", "--file", "x")
	if !errors.Is(err, backend.ErrContainerUnavailable) || calls != 1 {
		t.Fatalf("calls %d, error %v", calls, err)
	}
	if _, err := os.Stat("secrets.yaml"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed init wrote manifest")
	}
}
