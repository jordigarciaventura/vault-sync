package updater

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// BrewFormula is the fully-qualified Homebrew name of vault-sync.
const BrewFormula = "jordigarciaventura/tap/vault-sync"

// binaryName is the executable inside release archives.
const binaryName = "vault-sync"

// maxDownload bounds release archive downloads.
const maxDownload = 100 << 20

// Upgrader replaces the running binary with the latest release.
type Upgrader struct {
	Checker *Checker
	Out     io.Writer
	// DownloadBase defaults to https://github.com.
	DownloadBase string
	// Executable defaults to os.Executable.
	Executable func() (string, error)
	// RunBrew defaults to running `brew upgrade` attached to the terminal.
	RunBrew func(ctx context.Context, args ...string) error
}

// IsHomebrew reports whether exe lives inside a Homebrew prefix.
func IsHomebrew(exe string) bool {
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	exe = filepath.ToSlash(exe)
	return strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "/Caskroom/")
}

// Upgrade installs the latest stable release if it is newer than the running
// version. Homebrew installations are upgraded through brew so the package
// manager stays consistent; other installations are replaced in place after
// verifying the release checksum.
func (u *Upgrader) Upgrade(ctx context.Context) error {
	latest, err := u.Checker.Latest(ctx)
	if err != nil {
		return fmt.Errorf("check latest release: %w", err)
	}
	current := u.Checker.Current
	if semver.IsValid(Normalize(current)) && !Newer(current, latest.Version) {
		_, _ = fmt.Fprintf(u.Out, "vault-sync %s is already the latest version.\n", Normalize(current))
		_ = u.Checker.writeCache(cacheEntry{CheckedAt: u.Checker.now(), Latest: latest})
		return nil
	}

	exeFn := u.Executable
	if exeFn == nil {
		exeFn = os.Executable
	}
	exe, err := exeFn()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}

	if IsHomebrew(exe) {
		_, _ = fmt.Fprintf(u.Out, "Upgrading vault-sync to %s with Homebrew...\n", latest.Version)
		run := u.RunBrew
		if run == nil {
			run = runBrew
		}
		if err := run(ctx, "update"); err != nil {
			return err
		}
		return run(ctx, "upgrade", BrewFormula)
	}

	_, _ = fmt.Fprintf(u.Out, "Upgrading vault-sync %s → %s...\n", Normalize(current), latest.Version)
	if err := u.replace(ctx, exe, latest.Version); err != nil {
		return err
	}
	_ = u.Checker.writeCache(cacheEntry{CheckedAt: u.Checker.now(), Latest: latest})
	_, _ = fmt.Fprintf(u.Out, "Upgraded to %s.\n", latest.Version)
	return nil
}

func runBrew(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "brew", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("brew %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// ArchiveName returns the release archive name for a platform. It must match
// archives.name_template in .goreleaser.yaml.
func ArchiveName(version, goos, goarch string) string {
	return fmt.Sprintf("%s_%s_%s_%s.tar.gz", binaryName, strings.TrimPrefix(version, "v"), goos, goarch)
}

func (u *Upgrader) replace(ctx context.Context, exe, version string) error {
	base := u.DownloadBase
	if base == "" {
		base = "https://github.com"
	}
	prefix := fmt.Sprintf("%s/%s/releases/download/%s/", base, Repository, version)
	archive := ArchiveName(version, runtime.GOOS, runtime.GOARCH)

	sums, err := download(ctx, prefix+"checksums.txt")
	if err != nil {
		return err
	}
	want, err := checksumFor(sums, archive)
	if err != nil {
		return err
	}
	data, err := download(ctx, prefix+archive)
	if err != nil {
		return err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s; aborting upgrade", archive)
	}
	bin, err := extractBinary(data)
	if err != nil {
		return err
	}

	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".vault-sync-upgrade-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s (try with sudo or reinstall): %w", exe, err)
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
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), exe)
}

func download(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vault-sync-self-upgrade")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownload {
		return nil, fmt.Errorf("download %s: too large", url)
	}
	return data, nil
}

func checksumFor(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum published for %s (unsupported platform %s/%s?)", name, runtime.GOOS, runtime.GOARCH)
}

func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s not found in release archive", binaryName)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == binaryName {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}
