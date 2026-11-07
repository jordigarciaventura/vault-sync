package toolinstall

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipped(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(content)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tarred(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(content)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

type fakeGitHub struct {
	releases []map[string]any
	assets   map[string][]byte
	requests []string
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.URL.Path)
	switch {
	case strings.HasSuffix(r.URL.Path, "/releases/latest"):
		_ = json.NewEncoder(w).Encode(f.releases[0])
	case strings.HasSuffix(r.URL.Path, "/releases"):
		_ = json.NewEncoder(w).Encode(f.releases)
	case strings.HasPrefix(r.URL.Path, "/download/"):
		data, ok := f.assets[strings.TrimPrefix(r.URL.Path, "/download/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (f *fakeGitHub) release(base, tag string, prerelease bool, assets map[string][]byte, digests map[string]string) {
	var list []map[string]any
	for name, data := range assets {
		f.assets[name] = data
		d, ok := digests[name]
		if !ok {
			d = digest(data)
		}
		list = append(list, map[string]any{"name": name, "browser_download_url": base + "/download/" + name, "size": len(data), "digest": d})
	}
	f.releases = append(f.releases, map[string]any{"tag_name": tag, "prerelease": prerelease, "assets": list})
}

var bwTool = Tool{
	Name: "Bitwarden CLI", Binary: "bw", PathEnv: "BW_PATH", BrewFormula: "bitwarden-cli",
	Repo: "bitwarden/clients", TagPrefix: "cli-v",
	Asset: func(v, goos, goarch string) (string, bool) {
		if goos == "windows" {
			return "", false
		}
		return fmt.Sprintf("bw-%s-%s.zip", goos, v), true
	},
	ManualInstall: "`brew install bitwarden-cli`",
}

var infTool = Tool{
	Name: "Infisical CLI", Binary: "infisical", BrewFormula: "infisical", Repo: "Infisical/cli",
	Asset: func(v, goos, goarch string) (string, bool) {
		return fmt.Sprintf("cli_%s_%s_%s.tar.gz", v, goos, goarch), true
	},
	ManualInstall: "`brew install infisical`",
}

type harness struct {
	i     *Installer
	gh    *fakeGitHub
	srv   *httptest.Server
	log   *bytes.Buffer
	env   map[string]string
	path  map[string]string // binary -> path found by LookPath
	brews [][]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{gh: &fakeGitHub{assets: map[string][]byte{}}, log: &bytes.Buffer{}, env: map[string]string{}, path: map[string]string{}}
	h.srv = httptest.NewServer(h.gh)
	t.Cleanup(h.srv.Close)
	h.i = &Installer{
		Log:     h.log,
		APIBase: h.srv.URL,
		HTTP:    h.srv.Client(),
		Dir:     filepath.Join(t.TempDir(), "bin"),
		Getenv:  func(k string) string { return h.env[k] },
		LookPath: func(b string) (string, error) {
			if p, ok := h.path[b]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		RunBrew: func(_ context.Context, args ...string) error {
			h.brews = append(h.brews, args)
			h.path["bw"] = "/opt/homebrew/bin/bw"
			return nil
		},
		GOOS:   "linux",
		GOARCH: "amd64",
	}
	return h
}

func TestFindPrefersOverrideAndPath(t *testing.T) {
	h := newHarness(t)
	h.env["BW_PATH"] = "/custom/bw"
	h.path["bw"] = "/usr/bin/bw"
	if p, err := h.i.Find(context.Background(), bwTool); err != nil || p != "/custom/bw" {
		t.Errorf("override: %q, %v", p, err)
	}
	delete(h.env, "BW_PATH")
	if p, err := h.i.Find(context.Background(), bwTool); err != nil || p != "/usr/bin/bw" {
		t.Errorf("PATH: %q, %v", p, err)
	}
	if len(h.gh.requests) != 0 || len(h.brews) != 0 {
		t.Error("nothing should be installed")
	}
}

func TestFindUsesPreviousDownload(t *testing.T) {
	h := newHarness(t)
	dest := filepath.Join(h.i.Dir, "bw")
	if err := writeExecutable(dest, []byte("#!/bin/sh\n")); err != nil {
		t.Fatal(err)
	}
	if p, err := h.i.Find(context.Background(), bwTool); err != nil || p != dest {
		t.Errorf("%q, %v", p, err)
	}
	if len(h.gh.requests) != 0 {
		t.Error("should not download again")
	}
}

func TestInstallWithHomebrew(t *testing.T) {
	h := newHarness(t)
	h.path["brew"] = "/opt/homebrew/bin/brew"
	p, err := h.i.Find(context.Background(), bwTool)
	if err != nil || p != "/opt/homebrew/bin/bw" {
		t.Fatalf("%q, %v", p, err)
	}
	if len(h.brews) != 1 || strings.Join(h.brews[0], " ") != "install bitwarden-cli" {
		t.Errorf("brew calls = %v", h.brews)
	}
	if !strings.Contains(h.log.String(), "brew install bitwarden-cli") || len(h.gh.requests) != 0 {
		t.Errorf("log = %q, requests = %v", h.log, h.gh.requests)
	}
}

func TestDownloadZipWithTagPrefix(t *testing.T) {
	h := newHarness(t)
	bin := []byte("#!/bin/sh\necho bw\n")
	// Newest first: a prerelease and another product's release are skipped.
	h.gh.release(h.srv.URL, "cli-v2026.11.0", true, map[string][]byte{"bw-linux-2026.11.0.zip": zipped(t, "bw", []byte("rc"))}, nil)
	h.gh.release(h.srv.URL, "desktop-v2026.10.0", false, nil, nil)
	h.gh.release(h.srv.URL, "cli-v2026.10.1", false, map[string][]byte{"bw-linux-2026.10.1.zip": zipped(t, "bw", bin)}, nil)

	p, err := h.i.Find(context.Background(), bwTool)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(h.i.Dir, "bw") {
		t.Errorf("path = %q", p)
	}
	got, _ := os.ReadFile(p)
	info, _ := os.Stat(p)
	if !bytes.Equal(got, bin) || info.Mode().Perm() != 0o755 {
		t.Errorf("installed %q with mode %v", got, info.Mode())
	}
	if !strings.Contains(h.log.String(), "downloading bw 2026.10.1") {
		t.Errorf("log = %q", h.log)
	}
	if strings.Join(h.gh.requests, ",") != "/repos/bitwarden/clients/releases,/download/bw-linux-2026.10.1.zip" {
		t.Errorf("requests = %v", h.gh.requests)
	}
}

func TestDownloadTarGzLatest(t *testing.T) {
	h := newHarness(t)
	h.i.GOARCH = "arm64"
	bin := []byte("infisical-binary")
	h.gh.release(h.srv.URL, "v0.45.4", false, map[string][]byte{
		"cli_0.45.4_linux_arm64.tar.gz": tarred(t, map[string][]byte{"README.md": []byte("x"), "infisical": bin}),
	}, nil)
	p, err := h.i.Find(context.Background(), infTool)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, bin) {
		t.Errorf("installed %q", got)
	}
}

func TestDownloadFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		digest string
		goos   string
		want   string
	}{
		"checksum mismatch":    {digest: "sha256:" + strings.Repeat("0", 64), want: "checksum mismatch"},
		"no digest":            {digest: "", want: "no SHA-256 digest"},
		"unsupported platform": {goos: "windows", want: "no release is published for windows/amd64"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			if tc.goos != "" {
				h.i.GOOS = tc.goos
			}
			asset := "bw-linux-1.0.0.zip"
			h.gh.release(h.srv.URL, "cli-v1.0.0", false, map[string][]byte{asset: zipped(t, "bw", []byte("x"))}, map[string]string{asset: tc.digest})
			_, err := h.i.Find(context.Background(), bwTool)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "brew install bitwarden-cli") || !strings.Contains(err.Error(), "BW_PATH") {
				t.Errorf("err = %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(h.i.Dir, "bw")); statErr == nil {
				t.Error("nothing should be installed")
			}
		})
	}
}

func TestExtractMissingBinary(t *testing.T) {
	if _, err := extract("a.zip", zipped(t, "other", []byte("x")), "bw"); err == nil {
		t.Error("expected an error")
	}
	if _, err := extract("a.rar", nil, "bw"); err == nil {
		t.Error("expected an error")
	}
}

func TestDefaultDir(t *testing.T) {
	if d := DefaultDir(func(string) string { return "/xdg" }); d != filepath.Join("/xdg", "vault-sync", "bin") {
		t.Error(d)
	}
	if d := DefaultDir(func(string) string { return "relative" }); !strings.HasSuffix(d, filepath.Join(".local", "share", "vault-sync", "bin")) {
		t.Error(d)
	}
}
