package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v1.2.0", "v1.3.0", true},
		{"1.2.0", "v1.2.1", true},
		{"v1.2.0", "v1.2.0", false},
		{"v1.3.0", "v1.2.0", false},
		{"v1.2.0", "v1.3.0-rc1", false},
		{"dev", "v1.3.0", false},
		{"v1.2.0", "", false},
	}
	for _, tt := range tests {
		if got := Newer(tt.current, tt.latest); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v", tt.current, tt.latest, got)
		}
	}
}

func TestNotice(t *testing.T) {
	n := Notice("1.2.0", "v1.3.0")
	for _, want := range []string{"Installed: v1.2.0", "Latest:    v1.3.0", "brew upgrade vault-sync"} {
		if !strings.Contains(n, want) {
			t.Errorf("notice missing %q:\n%s", want, n)
		}
	}
}

func releaseServer(t *testing.T, tag string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/repos/"+Repository+"/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://example/%s"}`, tag, tag)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCacheLifecycle(t *testing.T) {
	var hits atomic.Int32
	srv := releaseServer(t, "v1.3.0", &hits)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &Checker{
		Current:   "v1.2.0",
		CacheFile: filepath.Join(t.TempDir(), "c", "update.json"),
		TTL:       12 * time.Hour,
		APIBase:   srv.URL,
		Now:       func() time.Time { return now },
	}

	if _, fresh := c.Cached(); fresh {
		t.Fatal("empty cache reported fresh")
	}
	rel, err := c.Refresh(context.Background())
	if err != nil || rel.Version != "v1.3.0" {
		t.Fatalf("Refresh = %+v, %v", rel, err)
	}
	cached, fresh := c.Cached()
	if !fresh || cached.Version != "v1.3.0" {
		t.Fatalf("Cached = %+v, %v", cached, fresh)
	}

	now = now.Add(13 * time.Hour)
	if _, fresh := c.Cached(); fresh {
		t.Error("cache should have expired")
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d", hits.Load())
	}
}

func TestRefreshFailureKeepsPreviousAndRecordsTime(t *testing.T) {
	var hits atomic.Int32
	srv := releaseServer(t, "v1.3.0", &hits)
	now := time.Now()
	c := &Checker{Current: "v1.0.0", CacheFile: filepath.Join(t.TempDir(), "u.json"), TTL: time.Hour, APIBase: srv.URL, Now: func() time.Time { return now }}
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	c.APIBase = "http://127.0.0.1:1" // unreachable
	if _, err := c.Refresh(context.Background()); err == nil {
		t.Fatal("expected network error")
	}
	rel, fresh := c.Cached()
	if !fresh || rel.Version != "v1.3.0" {
		t.Errorf("Cached = %+v fresh=%v", rel, fresh)
	}
}

func TestLatestRejectsPrerelease(t *testing.T) {
	var hits atomic.Int32
	srv := releaseServer(t, "v2.0.0-beta.1", &hits)
	c := &Checker{APIBase: srv.URL}
	if _, err := c.Latest(context.Background()); err == nil {
		t.Error("expected error for prerelease")
	}
}

func TestEnabled(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv(DisableEnv, "")
	if !Enabled("v1.0.0") {
		t.Error("should be enabled")
	}
	if Enabled("dev") {
		t.Error("dev builds should not check")
	}
	t.Setenv(DisableEnv, "1")
	if Enabled("v1.0.0") {
		t.Error("env var should disable")
	}
}

func TestTTLFromEnv(t *testing.T) {
	t.Setenv(TTLEnv, "30m")
	if c := NewChecker("v1.0.0"); c.TTL != 30*time.Minute {
		t.Errorf("TTL = %v", c.TTL)
	}
}

func TestIsHomebrew(t *testing.T) {
	if !IsHomebrew("/opt/homebrew/Cellar/vault-sync/1.0.0/bin/vault-sync") {
		t.Error("Cellar path not detected")
	}
	if !IsHomebrew("/opt/homebrew/Caskroom/vault-sync/1.0.0/vault-sync") {
		t.Error("Caskroom path not detected")
	}
	if IsHomebrew("/usr/local/bin/vault-sync-not-linked") {
		t.Error("false positive")
	}
}

func tarball(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		data []byte
	}{{"README.md", []byte("readme")}, {binaryName, content}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(f.data)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestUpgradeReplacesBinary(t *testing.T) {
	archive := tarball(t, []byte("NEW BINARY"))
	sum := sha256.Sum256(archive)
	name := ArchiveName("v1.3.0", runtime.GOOS, runtime.GOARCH)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repository+"/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0"}`))
	})
	prefix := "/" + Repository + "/releases/download/v1.3.0/"
	mux.HandleFunc(prefix+"checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	})
	mux.HandleFunc(prefix+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	exe := filepath.Join(t.TempDir(), "vault-sync")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	u := &Upgrader{
		Checker:      &Checker{Current: "v1.2.0", APIBase: srv.URL, CacheFile: filepath.Join(t.TempDir(), "c.json")},
		Out:          &out,
		DownloadBase: srv.URL,
		Executable:   func() (string, error) { return exe, nil },
	}
	if err := u.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "NEW BINARY" {
		t.Errorf("binary = %q", b)
	}
}

func TestUpgradeChecksumMismatch(t *testing.T) {
	archive := tarball(t, []byte("EVIL"))
	name := ArchiveName("v1.3.0", runtime.GOOS, runtime.GOARCH)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repository+"/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0"}`))
	})
	prefix := "/" + Repository + "/releases/download/v1.3.0/"
	mux.HandleFunc(prefix+"checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\n", strings.Repeat("0", 64), name)
	})
	mux.HandleFunc(prefix+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	exe := filepath.Join(t.TempDir(), "vault-sync")
	_ = os.WriteFile(exe, []byte("OLD"), 0o755)
	u := &Upgrader{
		Checker: &Checker{Current: "v1.2.0", APIBase: srv.URL}, Out: &bytes.Buffer{},
		DownloadBase: srv.URL, Executable: func() (string, error) { return exe, nil },
	}
	if err := u.Upgrade(context.Background()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Error("binary replaced despite checksum mismatch")
	}
}

func TestUpgradeHomebrewDelegates(t *testing.T) {
	var hits atomic.Int32
	srv := releaseServer(t, "v1.3.0", &hits)
	var calls []string
	u := &Upgrader{
		Checker:    &Checker{Current: "v1.2.0", APIBase: srv.URL},
		Out:        &bytes.Buffer{},
		Executable: func() (string, error) { return "/opt/homebrew/Cellar/vault-sync/1.2.0/bin/vault-sync", nil },
		RunBrew: func(_ context.Context, args ...string) error {
			calls = append(calls, strings.Join(args, " "))
			return nil
		},
	}
	if err := u.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ";") != "update;upgrade "+BrewFormula {
		t.Errorf("brew calls = %v", calls)
	}
}

func TestUpgradeAlreadyLatest(t *testing.T) {
	var hits atomic.Int32
	srv := releaseServer(t, "v1.2.0", &hits)
	var out bytes.Buffer
	u := &Upgrader{Checker: &Checker{Current: "v1.2.0", APIBase: srv.URL}, Out: &out}
	if err := u.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already the latest") {
		t.Errorf("out = %q", out.String())
	}
}
