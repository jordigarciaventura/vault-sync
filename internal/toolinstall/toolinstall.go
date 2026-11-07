// Package toolinstall locates the provider CLIs backends depend on (bw,
// infisical, …) and installs them on first use, so vault-sync itself does not
// have to depend on every provider's tooling.
//
// Lookup order: the tool's override variable, PATH, then the copy vault-sync
// downloaded earlier. When the tool is missing it is installed with Homebrew
// if brew is available, and otherwise downloaded from the tool's official
// GitHub release after verifying the SHA-256 digest GitHub publishes for the
// asset.
//
// This package is provider-independent: each backend describes its own Tool.
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
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Tool describes an external CLI and how to install it.
type Tool struct {
	// Name is the human-readable name, e.g. "Bitwarden CLI".
	Name string
	// Binary is the executable name, e.g. "bw".
	Binary string
	// PathEnv names the variable that overrides the executable path.
	PathEnv string
	// BrewFormula is the Homebrew formula providing Binary.
	BrewFormula string
	// Repo is the GitHub repository publishing releases, e.g. "bitwarden/clients".
	Repo string
	// TagPrefix selects the newest stable release whose tag starts with it
	// (for repositories that release several products). Empty means the
	// repository's latest release.
	TagPrefix string
	// Asset returns the release asset for a platform. version is the tag
	// without TagPrefix and without a leading "v". ok is false when the
	// platform is not supported.
	Asset func(version, goos, goarch string) (name string, ok bool)
	// ManualInstall is shown when automatic installation fails.
	ManualInstall string
}

// maxDownload bounds release archive downloads and extracted binaries.
const maxDownload = 500 << 20

// Installer finds and installs tools. The zero value is not usable; use New.
type Installer struct {
	// Log receives progress messages (never secrets). Typically stderr.
	Log io.Writer
	// APIBase is the GitHub API endpoint.
	APIBase string
	HTTP    *http.Client
	// Dir holds downloaded tools.
	Dir      string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// RunBrew runs brew with its output sent to Log.
	RunBrew func(ctx context.Context, args ...string) error
	GOOS    string
	GOARCH  string
}

// New returns an installer with production defaults.
func New(log io.Writer) *Installer {
	i := &Installer{
		Log:      log,
		APIBase:  "https://api.github.com",
		HTTP:     &http.Client{Timeout: 10 * time.Minute},
		Dir:      DefaultDir(os.Getenv),
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		GOOS:     runtime.GOOS,
		GOARCH:   runtime.GOARCH,
	}
	i.RunBrew = func(ctx context.Context, args ...string) error {
		cmd := exec.CommandContext(ctx, "brew", args...)
		cmd.Stdout, cmd.Stderr = i.Log, i.Log
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("brew %s: %w", strings.Join(args, " "), err)
		}
		return nil
	}
	return i
}

// DefaultDir is where downloaded tools are kept:
// $XDG_DATA_HOME/vault-sync/bin, or ~/.local/share/vault-sync/bin.
func DefaultDir(getenv func(string) string) string {
	if d := getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "vault-sync", "bin")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "vault-sync", "bin")
}

// Find returns the path of t's executable, installing it when it is missing.
func Find(ctx context.Context, t Tool) (string, error) {
	return New(os.Stderr).Find(ctx, t)
}

// Find returns the path of t's executable, installing it when it is missing.
func (i *Installer) Find(ctx context.Context, t Tool) (string, error) {
	if t.PathEnv != "" {
		if p := i.Getenv(t.PathEnv); p != "" {
			return p, nil
		}
	}
	if p, err := i.LookPath(t.Binary); err == nil {
		return p, nil
	}
	if p := i.managed(t); p != "" && isExecutable(p) {
		return p, nil
	}
	p, err := i.install(ctx, t)
	if err != nil {
		hint := t.ManualInstall
		if t.PathEnv != "" {
			hint += " or set " + t.PathEnv
		}
		return "", fmt.Errorf("the %s (%s) is not installed and could not be installed automatically: %w; install it with %s",
			t.Name, t.Binary, err, hint)
	}
	return p, nil
}

func (i *Installer) managed(t Tool) string {
	if i.Dir == "" {
		return ""
	}
	return filepath.Join(i.Dir, t.Binary)
}

func isExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func (i *Installer) install(ctx context.Context, t Tool) (string, error) {
	if t.BrewFormula != "" {
		if _, err := i.LookPath("brew"); err == nil {
			return i.installBrew(ctx, t)
		}
	}
	return i.download(ctx, t)
}

func (i *Installer) installBrew(ctx context.Context, t Tool) (string, error) {
	_, _ = fmt.Fprintf(i.Log, "vault-sync needs the %s; installing it with Homebrew (brew install %s)...\n", t.Name, t.BrewFormula)
	if err := i.RunBrew(ctx, "install", t.BrewFormula); err != nil {
		return "", err
	}
	if p, err := i.LookPath(t.Binary); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("brew installed %s but %s is not on PATH", t.BrewFormula, t.Binary)
}

type release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

func (i *Installer) download(ctx context.Context, t Tool) (string, error) {
	if t.Repo == "" || t.Asset == nil {
		return "", errors.New("no download source is known")
	}
	dest := i.managed(t)
	if dest == "" {
		return "", errors.New("cannot determine the home directory")
	}
	rel, err := i.latest(ctx, t)
	if err != nil {
		return "", err
	}
	version := strings.TrimPrefix(strings.TrimPrefix(rel.TagName, t.TagPrefix), "v")
	name, ok := t.Asset(version, i.GOOS, i.GOARCH)
	if !ok {
		return "", fmt.Errorf("no release is published for %s/%s", i.GOOS, i.GOARCH)
	}
	idx := -1
	for n, a := range rel.Assets {
		if a.Name == name {
			idx = n
		}
	}
	if idx < 0 {
		return "", fmt.Errorf("release %s has no asset %s", rel.TagName, name)
	}
	asset := rel.Assets[idx]
	want, ok := strings.CutPrefix(asset.Digest, "sha256:")
	if !ok || len(want) != sha256.Size*2 {
		return "", fmt.Errorf("release asset %s has no SHA-256 digest to verify it against", name)
	}
	if asset.Size > maxDownload {
		return "", fmt.Errorf("release asset %s is too large", name)
	}

	_, _ = fmt.Fprintf(i.Log, "vault-sync needs the %s; downloading %s %s (%d MB)...\n", t.Name, t.Binary, version, (asset.Size+1<<20-1)>>20)
	data, err := i.get(ctx, asset.URL, maxDownload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != strings.ToLower(want) {
		return "", fmt.Errorf("checksum mismatch for %s; nothing was installed", name)
	}
	bin, err := extract(name, data, t.Binary)
	if err != nil {
		return "", err
	}
	if err := writeExecutable(dest, bin); err != nil {
		return "", err
	}
	_, _ = fmt.Fprintf(i.Log, "Installed %s to %s.\n\n", t.Binary, dest)
	return dest, nil
}

// latest returns the newest stable release matching t.TagPrefix.
func (i *Installer) latest(ctx context.Context, t Tool) (release, error) {
	if t.TagPrefix == "" {
		var r release
		raw, err := i.get(ctx, fmt.Sprintf("%s/repos/%s/releases/latest", i.APIBase, t.Repo), 10<<20)
		if err == nil {
			err = json.Unmarshal(raw, &r)
		}
		return r, err
	}
	var rs []release
	raw, err := i.get(ctx, fmt.Sprintf("%s/repos/%s/releases?per_page=100", i.APIBase, t.Repo), 50<<20)
	if err != nil {
		return release{}, err
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return release{}, fmt.Errorf("unexpected response from the GitHub API: %w", err)
	}
	for _, r := range rs { // newest first
		if !r.Draft && !r.Prerelease && strings.HasPrefix(r.TagName, t.TagPrefix) {
			return r, nil
		}
	}
	return release{}, fmt.Errorf("no %s* release found in %s", t.TagPrefix, t.Repo)
}

func (i *Installer) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vault-sync")
	if strings.HasPrefix(url, i.APIBase) {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := i.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: response too large", url)
	}
	return data, nil
}

// extract returns the regular file named binary from a .zip or .tar.gz.
func extract(archive string, data []byte, binary string) ([]byte, error) {
	switch {
	case strings.HasSuffix(archive, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Mode().IsRegular() && path.Base(f.Name) == binary {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer func() { _ = rc.Close() }()
				return readLimited(rc)
			}
		}
	case strings.HasSuffix(archive, ".tar.gz"), strings.HasSuffix(archive, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag == tar.TypeReg && path.Base(h.Name) == binary {
				return readLimited(tr)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported archive format: %s", archive)
	}
	return nil, fmt.Errorf("%s not found in %s", binary, archive)
}

func readLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxDownload+1))
	if err == nil && len(data) > maxDownload {
		err = errors.New("archived binary is too large")
	}
	return data, err
}

// writeExecutable atomically installs bin at dest with mode 0755.
func writeExecutable(dest string, bin []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+"-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(bin); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}
