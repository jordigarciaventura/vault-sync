package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const root = "/repo"

var known = Options{KnownBackends: []string{"bitwarden"}}

func TestParseValid(t *testing.T) {
	cfg, err := Parse([]byte(`
backend:
  type: bitwarden
  account: jordigarciaventura
project: Example
files:
  - path: /shared/keys/firmware-signing.key
    permissions: "0600"
  - path: backend/.env
  - path: ./backend/.env.production
    permissions: 0640
`), root, known)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Backend.Type != "bitwarden" || cfg.Backend.Account != "jordigarciaventura" {
		t.Errorf("backend = %+v", cfg.Backend)
	}
	if cfg.Project != "Example" {
		t.Errorf("project = %q", cfg.Project)
	}
	want := []File{
		{Path: "/shared/keys/firmware-signing.key", Name: "shared/keys/firmware-signing.key", Destination: "/shared/keys/firmware-signing.key", Absolute: true, Permissions: 0o600},
		{Path: "backend/.env", Name: "backend/.env", Destination: "/repo/backend/.env", Permissions: 0o600},
		{Path: "./backend/.env.production", Name: "backend/.env.production", Destination: "/repo/backend/.env.production", Permissions: 0o640},
	}
	if len(cfg.Files) != len(want) {
		t.Fatalf("got %d files, want %d", len(cfg.Files), len(want))
	}
	for i := range want {
		if cfg.Files[i] != want[i] {
			t.Errorf("files[%d] = %+v, want %+v", i, cfg.Files[i], want[i])
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"empty document", ``, "config is empty"},
		{"invalid yaml", `backend: [`, "invalid YAML"},
		{"unknown field", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: a}]\nextra: 1", "field extra not found"},
		{"missing backend", "project: p\nfiles: [{path: a}]", "backend: section is required"},
		{"missing backend type", "backend: {account: x}\nproject: p\nfiles: [{path: a}]", "backend.type: is required"},
		{"unknown backend", "backend: {type: vault}\nproject: p\nfiles: [{path: a}]", `unknown backend "vault" (supported: bitwarden)`},
		{"missing project", "backend: {type: bitwarden}\nfiles: [{path: a}]", "project: is required"},
		{"empty files", "backend: {type: bitwarden}\nproject: p\nfiles: []", "at least one file is required"},
		{"missing path", "backend: {type: bitwarden}\nproject: p\nfiles: [{permissions: '0600'}]", "path: is required"},
		{"escaping path", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: ../secret}]", "must stay inside"},
		{"escaping nested path", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: a/../../secret}]", "must stay inside"},
		{"dot path", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: .}]", "does not name a file"},
		{"directory path", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: dir/}]", "must be a file"},
		{"tilde path", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: ~/x}]", "not expanded"},
		{"absolute dotdot", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: /etc/../x}]", "must not contain '..'"},
		{"duplicate", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: a/b}, {path: ./a/b}]", "duplicates files[0]"},
		{"bad permissions", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: a, permissions: rw}]", "octal"},
		{"permissions out of range", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: a, permissions: '4755'}]", "setuid"},
		{"permissions not octal", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: a, permissions: '0699'}]", "octal"},
		{"unreadable permissions", "backend: {type: bitwarden}\nproject: p\nfiles: [{path: a, permissions: '0200'}]", "owner must be able to read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml), root, known)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestParseReportsAllProblems(t *testing.T) {
	_, err := Parse([]byte("backend: {type: nope}\nfiles: []"), root, known)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(ve.Problems) != 3 {
		t.Errorf("got %d problems, want 3: %v", len(ve.Problems), ve.Problems)
	}
}

func TestParsePermissions(t *testing.T) {
	tests := map[string]fs.FileMode{"": 0o600, "0600": 0o600, "600": 0o600, "0o644": 0o644, "0755": 0o755, "0400": 0o400}
	for in, want := range tests {
		got, err := ParsePermissions(in)
		if err != nil || got != want {
			t.Errorf("ParsePermissions(%q) = %o, %v; want %o", in, got, err, want)
		}
	}
}

func TestLoadAndFind(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, DefaultFileName)
	if err := os.WriteFile(manifest, []byte("backend: {type: bitwarden}\nproject: p\nfiles: [{path: x/.env}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	found, err := Find(sub)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found != manifest {
		t.Errorf("Find = %q, want %q", found, manifest)
	}

	cfg, err := Load(found, known)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Root != dir || cfg.Files[0].Destination != filepath.Join(dir, "x", ".env") {
		t.Errorf("relative path not resolved against manifest dir: root=%q dest=%q", cfg.Root, cfg.Files[0].Destination)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"), known)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFindNotFound(t *testing.T) {
	if _, err := Find(t.TempDir()); err == nil {
		t.Error("expected error")
	}
}

func TestParseBackendEnvironment(t *testing.T) {
	cfg, err := Parse([]byte("backend:\n  type: bitwarden\n  environment: \" prod \"\nproject: p\nfiles:\n  - path: .env\n"), root, known)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backend.Environment != "prod" {
		t.Errorf("environment = %q", cfg.Backend.Environment)
	}
}

func TestParseBackendServer(t *testing.T) {
	cfg, err := Parse([]byte("backend:\n  type: bitwarden\n  server: \" https://secrets.example.com \"\nproject: p\nfiles:\n  - path: .env\n"), root, known)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backend.Server != "https://secrets.example.com" {
		t.Errorf("server = %q", cfg.Backend.Server)
	}
}
