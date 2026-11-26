package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

type defaultsBackend struct {
	memBackend
	cfg backend.Config
}

func (b *defaultsBackend) SetupFields() []backend.SetupField {
	return []backend.SetupField{
		{Key: "server", Label: "Server", Default: "https://example.com"},
		{Key: "environment", Label: "Environment", Default: "dev"},
		{Key: "account", Label: "Account"},
		{Key: "region", Label: "Region", Default: "us-east-1"},
	}
}
func (b *defaultsBackend) InitializeProject(_ context.Context, project string) (backend.ProjectSetup, error) {
	return backend.ProjectSetup{Project: project, Config: b.cfg}, nil
}

func TestInitRemembersDefaultsAcrossRuns(t *testing.T) {
	t.Chdir(t.TempDir())
	configDir := t.TempDir()
	r := backend.NewRegistry()
	for _, typ := range []string{"alpha", "beta"} {
		r.Register(typ, func(cfg backend.Config) (backend.Backend, error) { return &defaultsBackend{cfg: cfg}, nil })
	}
	run := func(input string, args ...string) (string, error) {
		t.Helper()
		var out strings.Builder
		a := &app{registry: r, stdin: strings.NewReader(input), stdout: &out, stderr: &out, userConfigDir: func() (string, error) { return configDir, nil }}
		cmd := newRootCommand(a)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	if out, err := run("first/secrets.yaml\nbeta\nhttps://custom.example\nprod\n123\neu-west-1\nvault-sync/nested/example\n.env\n\n", "init"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := run("second/secrets.yaml\n\n\n\n\n\n\n.env\n\n", "init")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"Backend [beta]", "Server [https://custom.example]", "Environment [prod]", "Account [123]", "Region [eu-west-1]", "[vault-sync/nested/second]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	name := filepath.Join(configDir, "vault-sync", "init-defaults.yaml")
	before, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(name)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions %o", info.Mode().Perm())
	}
	// Cancellation and flag-driven runs must not modify saved preferences.
	if _, err := run("cancelled/secrets.yaml\nalpha\n", "init"); err == nil {
		t.Fatal("expected cancellation")
	}
	if out, err := run("", "init", "--non-interactive", "--config", "flags.yaml", "--backend", "alpha", "--project", "flags", "--file", "file"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	after, _ := os.ReadFile(name)
	if string(before) != string(after) {
		t.Fatal("preferences changed")
	}
	out, err = run("third/secrets.yaml\nalpha\n\n\nother\n\nplain\n.env\n\n", "init")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Server [https://example.com]") || !strings.Contains(out, "[third]") {
		t.Fatalf("provider defaults leaked: %s", out)
	}
	prefs, _, err := (&app{userConfigDir: func() (string, error) { return configDir, nil }}).loadInitDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Providers["alpha"].ProjectPrefix != "" || prefs.Providers["beta"].ProjectPrefix != "vault-sync/nested/" {
		t.Fatalf("prefixes: %+v", prefs)
	}
	// A damaged preference file falls back without blocking init.
	if err := os.WriteFile(name, []byte("[invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = run("fourth/secrets.yaml\nalpha\n\n\nx\n\nplain\n.env\n\n", "init")
	if err != nil || !strings.Contains(out, "could not load init defaults") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestInitRemembersManifestPath(t *testing.T) {
	t.Chdir(t.TempDir())
	configDir := t.TempDir()
	run := func(input string) (string, error) {
		r := backend.NewRegistry()
		r.Register("memory", memFactory)
		var out strings.Builder
		a := &app{registry: r, stdin: strings.NewReader(input), stdout: &out, stderr: &out, userConfigDir: func() (string, error) { return configDir, nil }}
		cmd := newRootCommand(a)
		cmd.SetArgs([]string{"init"})
		err := cmd.Execute()
		return out.String(), err
	}
	if out, err := run("vault.yaml\n\nproject\nsecret.txt\n\n"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	a := &app{userConfigDir: func() (string, error) { return configDir, nil }}
	prefs, _, err := a.loadInitDefaults()
	if err != nil || prefs.Manifest != "vault.yaml" {
		t.Fatalf("prefs=%+v err=%v", prefs, err)
	}
	// Accepting the remembered path must still ask before overwriting it.
	out, err := run("\ny\n\nproject\nother.txt\n\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"secrets.yaml", "vault.yaml", "Manifest file [vault.yaml]", "already exists. Overwrite"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if _, err := os.Stat("secrets.yaml"); !os.IsNotExist(err) {
		t.Fatal("created built-in path instead of remembered path")
	}
	// Cancelling another path must preserve the saved choice.
	if _, err := run("other.yaml\n"); err == nil {
		t.Fatal("expected incomplete input error")
	}
	prefs, _, err = a.loadInitDefaults()
	if err != nil || prefs.Manifest != "vault.yaml" {
		t.Fatalf("cancel changed prefs=%+v err=%v", prefs, err)
	}
}
