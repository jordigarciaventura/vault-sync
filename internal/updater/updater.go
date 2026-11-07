// Package updater checks GitHub Releases for newer vault-sync versions and
// implements self-upgrade.
//
// The check never blocks commands: notifications are rendered from a local
// cache, and an expired cache is refreshed by a detached background process.
// Only the repository name and a generic User-Agent are sent to GitHub.
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Repository is the GitHub repository releases are published to.
const Repository = "jordigarciaventura/vault-sync"

// Environment variables controlling the update check.
const (
	DisableEnv = "VAULT_SYNC_NO_UPDATE_CHECK"
	TTLEnv     = "VAULT_SYNC_UPDATE_CHECK_TTL"
)

// DefaultTTL is how long a check result stays valid.
const DefaultTTL = 12 * time.Hour

// Timeout bounds every network request made by the update check.
const Timeout = 3 * time.Second

// Release is a published GitHub Release.
type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

type cacheEntry struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    Release   `json:"latest"`
}

// Checker performs and caches release checks.
type Checker struct {
	Current   string
	CacheFile string
	TTL       time.Duration
	APIBase   string // default https://api.github.com
	Client    *http.Client
	Now       func() time.Time
}

// NewChecker returns a Checker configured from the environment.
func NewChecker(current string) *Checker {
	c := &Checker{Current: current, TTL: DefaultTTL}
	if dir, err := os.UserCacheDir(); err == nil {
		c.CacheFile = filepath.Join(dir, "vault-sync", "update-check.json")
	}
	if v := os.Getenv(TTLEnv); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			c.TTL = d
		}
	}
	return c
}

// Enabled reports whether automatic update checks should run.
func Enabled(current string) bool {
	if v := os.Getenv(DisableEnv); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		return false
	}
	if os.Getenv("CI") != "" {
		return false
	}
	return semver.IsValid(current)
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Cached returns the cached latest release and whether the cache is still
// fresh. It never touches the network.
func (c *Checker) Cached() (Release, bool) {
	e, err := c.readCache()
	if err != nil {
		return Release{}, false
	}
	return e.Latest, c.now().Sub(e.CheckedAt) < c.TTL
}

// Refresh fetches the latest release and stores it in the cache. The check
// time is recorded even on failure so an offline machine does not retry on
// every command.
func (c *Checker) Refresh(ctx context.Context) (Release, error) {
	prev, _ := c.readCache()
	rel, err := c.Latest(ctx)
	entry := cacheEntry{CheckedAt: c.now(), Latest: prev.Latest}
	if err == nil {
		entry.Latest = rel
	}
	if werr := c.writeCache(entry); werr != nil && err == nil {
		err = werr
	}
	return rel, err
}

// Latest queries GitHub for the latest stable release (drafts and
// pre-releases are excluded by the endpoint).
func (c *Checker) Latest(ctx context.Context) (Release, error) {
	base := c.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: Timeout}
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+Repository+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "vault-sync-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub API returned %s", resp.Status)
	}
	var body struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("decode GitHub response: %w", err)
	}
	v := Normalize(body.TagName)
	if !semver.IsValid(v) || body.Draft || body.Prerelease || semver.Prerelease(v) != "" {
		return Release{}, fmt.Errorf("latest release %q is not a stable semantic version", body.TagName)
	}
	return Release{Version: v, URL: body.HTMLURL}, nil
}

// Normalize ensures a leading "v".
func Normalize(v string) string {
	v = strings.TrimSpace(v)
	if v != "" && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// Newer reports whether latest is a stable release newer than current.
func Newer(current, latest string) bool {
	current, latest = Normalize(current), Normalize(latest)
	if !semver.IsValid(current) || !semver.IsValid(latest) || semver.Prerelease(latest) != "" {
		return false
	}
	return semver.Compare(latest, current) > 0
}

// Notice formats the update notification.
func Notice(current, latest string) string {
	return fmt.Sprintf(`A new version of vault-sync is available!

Installed: %s
Latest:    %s

Update with:
brew upgrade vault-sync
`, Normalize(current), Normalize(latest))
}

func (c *Checker) readCache() (cacheEntry, error) {
	var e cacheEntry
	if c.CacheFile == "" {
		return e, errors.New("no cache directory")
	}
	data, err := os.ReadFile(c.CacheFile)
	if err != nil {
		return e, err
	}
	err = json.Unmarshal(data, &e)
	return e, err
}

func (c *Checker) writeCache(e cacheEntry) error {
	if c.CacheFile == "" {
		return errors.New("no cache directory")
	}
	if err := os.MkdirAll(filepath.Dir(c.CacheFile), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.CacheFile), ".update-check-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.CacheFile)
}
