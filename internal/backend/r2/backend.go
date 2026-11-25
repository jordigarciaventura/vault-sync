// Package r2 stores exact file bytes in Cloudflare R2 using Wrangler OAuth and
// the Cloudflare REST API. Credentials never appear in command arguments.
package r2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

// Type is the backend identifier in secrets.yaml.
const Type = "r2"

// Backend stores files using a Cloudflare-managed browser session or API token.
type Backend struct {
	account     string
	token       string
	apiBase     string
	http        *http.Client
	run         func(context.Context, bool, ...string) ([]byte, error)
	interactive func() bool
	getenv      func(string) string
}

var (
	_              backend.Backend        = (*Backend)(nil)
	_              backend.FileLister     = (*Backend)(nil)
	_              backend.AccountManager = (*Backend)(nil)
	accountPattern                        = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)
)

// New only validates settings; CLI resolution and authentication are lazy.
func New(cfg backend.Config) (backend.Backend, error) {
	if cfg.Account != "" && !accountPattern.MatchString(cfg.Account) {
		return nil, errors.New("backend.account: use your 32-character Cloudflare account ID")
	}
	if cfg.Server != "" || cfg.Environment != "" || (cfg.Region != "" && cfg.Region != "auto") {
		return nil, errors.New("R2 uses the Cloudflare API; remove backend.server, backend.environment and any backend.region other than auto")
	}
	return &Backend{account: cfg.Account, apiBase: "https://api.cloudflare.com/client/v4", http: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, run: newRunner(), getenv: os.Getenv, interactive: func() bool { return !cfg.NonInteractive && term.IsTerminal(int(os.Stdin.Fd())) }}, nil
}

// Name implements backend.Backend.
func (b *Backend) Name() string { return "Cloudflare R2" }

// Close discards the in-memory token; Wrangler retains its own session.
func (b *Backend) Close() error { b.token = ""; return nil }

func (b *Backend) loadToken(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if token := strings.TrimSpace(b.getenv("CLOUDFLARE_API_TOKEN")); token != "" {
		if strings.ContainsAny(token, " \t\r\n") {
			return fmt.Errorf("%w: invalid CLOUDFLARE_API_TOKEN", backend.ErrAuthentication)
		}
		b.token = token
		return nil
	}
	out, err := b.run(ctx, false, "auth", "token", "--json")
	if err != nil {
		return err
	}
	var result struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if json.Unmarshal(out, &result) != nil || (result.Type != "oauth" && result.Type != "api_token") || result.Token == "" || strings.ContainsAny(result.Token, " \t\r\n") {
		return fmt.Errorf("%w: Wrangler returned no usable token; run `wrangler login` or upgrade Wrangler to a version supporting `auth token --json`", backend.ErrAuthentication)
	}
	b.token = result.Token
	return nil
}

// Authenticate reuses Wrangler's refreshable OAuth session, opening browser login
// only when credentials are unavailable and a terminal is allowed.
func (b *Backend) Authenticate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !accountPattern.MatchString(b.account) {
		return fmt.Errorf("%w: backend.account is required for R2; set your Cloudflare account ID", backend.ErrAuthentication)
	}
	return b.ensureToken(ctx)
}

func (b *Backend) ensureToken(ctx context.Context) error {
	if b.token != "" {
		return nil
	}
	if err := b.loadToken(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, backend.ErrAuthentication) {
			return err
		}
		if !b.interactive() || b.getenv("CLOUDFLARE_API_TOKEN") != "" {
			return err
		}
		if _, err := b.run(ctx, true, "login"); err != nil {
			return err
		}
		return b.loadToken(ctx)
	}
	return nil
}

// Status inspects credentials without starting browser login.
func (b *Backend) Status(ctx context.Context) (backend.AccountStatus, error) {
	err := b.loadToken(ctx)
	if err != nil {
		if errors.Is(err, backend.ErrAuthentication) {
			return backend.AccountStatus{}, nil
		}
		return backend.AccountStatus{}, err
	}
	return backend.AccountStatus{LoggedIn: true, User: b.account}, nil
}

// Login authenticates with environment credentials or Wrangler browser login.
func (b *Backend) Login(ctx context.Context) (backend.AccountStatus, error) {
	b.token = ""
	if err := b.ensureToken(ctx); err != nil {
		return backend.AccountStatus{}, err
	}
	return backend.AccountStatus{LoggedIn: true, User: b.account}, nil
}

// Logout ends Wrangler's OAuth session; environment tokens must be unset by the user.
func (b *Backend) Logout(ctx context.Context) error {
	b.token = ""
	if b.getenv("CLOUDFLARE_API_TOKEN") != "" {
		return errors.New("unset CLOUDFLARE_API_TOKEN to end environment authentication")
	}
	_, err := b.run(ctx, false, "logout")
	return err
}

func location(project, filename string) (string, string, error) {
	bucket, prefix, err := backend.SplitProject(project)
	if err != nil {
		return "", "", err
	}
	if _, _, err := backend.SplitProject(filename); err != nil {
		return "", "", fmt.Errorf("filename: %w", err)
	}
	if prefix != "" {
		filename = prefix + "/" + filename
	}
	return bucket, filename, nil
}

func (b *Backend) bucketPath(bucket string) string {
	return "/accounts/" + b.account + "/r2/buckets/" + url.PathEscape(bucket)
}
func objectPath(key string) string {
	parts := strings.Split(key, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// ResolveFile returns the exact object body, including empty and binary content.
func (b *Backend) ResolveFile(ctx context.Context, project, filename string) (io.ReadCloser, error) {
	bucket, key, err := location(project, filename)
	if err != nil {
		return nil, err
	}
	response, err := b.request(ctx, http.MethodGet, b.bucketPath(bucket)+"/objects/"+objectPath(key), nil, "")
	if errors.Is(err, backend.ErrNotFound) {
		return nil, backend.NotFoundError(project, filename, "check the object key and bucket")
	}
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

// StoreFile sends file bytes in the HTTP body, never through command arguments.
func (b *Backend) StoreFile(ctx context.Context, project, filename string, content []byte) error {
	bucket, key, err := location(project, filename)
	if err != nil {
		return err
	}
	response, err := b.request(ctx, http.MethodPut, b.bucketPath(bucket)+"/objects/"+objectPath(key), content, "application/octet-stream")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return decodeResponse(response, nil)
}

// ListFiles discovers all pages of canonical names relative to the project prefix.
func (b *Backend) ListFiles(ctx context.Context, project, directory string) ([]string, error) {
	bucket, prefix, err := backend.SplitProject(project)
	if err != nil {
		return nil, err
	}
	base := ""
	if prefix != "" {
		base = prefix + "/"
	}
	query := base
	if directory != "" && directory != "." {
		if _, _, err := backend.SplitProject(directory); err != nil {
			return nil, err
		}
		query += directory + "/"
	}
	names := []string{}
	cursor := ""
	seen := map[string]bool{}
	for {
		params := url.Values{"prefix": {query}, "per_page": {"1000"}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		response, err := b.request(ctx, http.MethodGet, b.bucketPath(bucket)+"/objects?"+params.Encode(), nil, "")
		if err != nil {
			return nil, err
		}
		var page struct {
			Success bool `json:"success"`
			Result  []struct {
				Key string `json:"key"`
			} `json:"result"`
			Info struct {
				Cursor    string `json:"cursor"`
				Truncated bool   `json:"is_truncated"`
			} `json:"result_info"`
		}
		err = json.NewDecoder(response.Body).Decode(&page)
		_ = response.Body.Close()
		if err != nil || !page.Success {
			return nil, errors.New("invalid R2 object listing response")
		}
		for _, object := range page.Result {
			if !strings.HasPrefix(object.Key, query) || strings.HasSuffix(object.Key, "/") {
				continue
			}
			name := strings.TrimPrefix(object.Key, base)
			if _, _, err := backend.SplitProject(name); err != nil {
				return nil, errors.New("R2 listing contains an unsafe object key; rename it before syncing")
			}
			names = append(names, name)
		}
		if !page.Info.Truncated {
			break
		}
		cursor = page.Info.Cursor
		if cursor == "" || seen[cursor] {
			return nil, errors.New("invalid R2 pagination cursor")
		}
		seen[cursor] = true
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}
