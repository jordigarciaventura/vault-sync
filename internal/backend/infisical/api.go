package infisical

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

// errFolderNotFound reports that a secret path does not exist.
var errFolderNotFound = errors.New("folder not found")

// apiError is a non-2xx answer from the Infisical API. Message is the
// server's own error message; the response body is never included otherwise.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("infisical api: HTTP %d", e.Status)
	}
	return fmt.Sprintf("infisical api: %s (HTTP %d)", e.Message, e.Status)
}

// api is a minimal client for the Infisical REST API.
type api struct {
	base  string // e.g. https://app.infisical.com/api
	token string
	http  *http.Client
}

func newAPI(domain, token string) *api {
	return &api{
		base:  strings.TrimSuffix(strings.TrimSuffix(domain, "/"), "/api") + "/api",
		token: token,
		http:  &http.Client{Timeout: 60 * time.Second},
	}
}

// do sends a JSON request and decodes a JSON answer into out (when non-nil).
func (a *api) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := a.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		// url.Error includes the URL, which holds no secrets.
		return fmt.Errorf("infisical api: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		// Do not include the body: it may contain secrets.
		return fmt.Errorf("infisical api: %s %s: unexpected response format", method, path)
	}
	return nil
}

// responseError builds an error from a failed response, keeping only the
// server's short error message.
func responseError(resp *http.Response) error {
	var body struct {
		Message json.RawMessage `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)
	msg := errorMessage(body.Message)
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	e := &apiError{Status: resp.StatusCode, Message: msg}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: %w; run `vault-sync login` or refresh %s", backend.ErrAuthentication, e, tokenEnv)
	}
	return e
}

// errorMessage extracts the server's message, which is a string or, on a
// validation failure (HTTP 422), an array of field-level issues. Only each
// issue's field path and message are kept: other issue fields may echo input.
func errorMessage(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var issues []struct {
		Path    []any  `json:"path"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &issues) != nil {
		return ""
	}
	parts := make([]string, 0, len(issues))
	for _, is := range issues {
		m := strings.TrimSpace(is.Message)
		if m == "" {
			continue
		}
		if len(is.Path) > 0 {
			field := make([]string, len(is.Path))
			for i, p := range is.Path {
				field[i] = fmt.Sprint(p)
			}
			m = strings.Join(field, ".") + ": " + m
		}
		parts = append(parts, m)
	}
	return strings.Join(parts, "; ")
}

func isStatus(err error, code int) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == code
}

type project struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Environments []struct {
		Slug string `json:"slug"`
	} `json:"environments"`
}

// listProjects returns the secrets management projects the token can access.
func (a *api) listProjects(ctx context.Context) ([]project, error) {
	var out struct {
		Projects []project `json:"projects"`
	}
	err := a.do(ctx, http.MethodGet, "/v1/projects", url.Values{"type": {"secret-manager"}}, nil, &out)
	return out.Projects, err
}

// createProject creates a secrets management project with the default
// environments. slug may be empty to let the server pick one.
func (a *api) createProject(ctx context.Context, name, slug string) (project, error) {
	body := map[string]any{"projectName": name, "type": "secret-manager"}
	if slug != "" {
		body["slug"] = slug
	}
	var out struct {
		Project project `json:"project"`
	}
	err := a.do(ctx, http.MethodPost, "/v1/projects", nil, body, &out)
	if err == nil && out.Project.ID == "" {
		err = errors.New("infisical api: project creation returned no id")
	}
	return out.Project, err
}

func (a *api) projectBySlug(ctx context.Context, slug string) (project, error) {
	var p project
	err := a.do(ctx, http.MethodGet, "/v1/projects/slug/"+url.PathEscape(slug), nil, nil, &p)
	if err == nil && p.ID == "" {
		err = errors.New("infisical api: project lookup returned no id")
	}
	return p, err
}

type secret struct {
	ID                string `json:"id"`
	Key               string `json:"secretKey"`
	Value             string `json:"secretValue"`
	Type              string `json:"type"`
	SecretValueHidden bool   `json:"secretValueHidden"`
}

// Secret types.
const typeShared = "shared"

// listSecrets returns the secrets stored directly in dir, unexpanded and
// without imports or personal overrides. It returns errFolderNotFound when
// dir does not exist.
func (a *api) listSecrets(ctx context.Context, projectID, env, dir string) ([]secret, error) {
	q := url.Values{
		"projectId":                {projectID},
		"environment":              {env},
		"secretPath":               {dir},
		"viewSecretValue":          {"true"},
		"expandSecretReferences":   {"false"},
		"includeImports":           {"false"},
		"recursive":                {"false"},
		"includePersonalOverrides": {"false"},
	}
	var out struct {
		Secrets []secret `json:"secrets"`
	}
	err := a.do(ctx, http.MethodGet, "/v4/secrets", q, nil, &out)
	if isStatus(err, http.StatusNotFound) && dir != "/" {
		return nil, errFolderNotFound
	}
	return out.Secrets, err
}

type writeSecret struct {
	ProjectID   string `json:"projectId"`
	Environment string `json:"environment"`
	SecretPath  string `json:"secretPath"`
	SecretValue string `json:"secretValue"`
	Type        string `json:"type"`
}

// writeResult is the answer to a create or update. Approval is set instead of
// Secret when a change policy requires someone to approve the write.
type writeResult struct {
	Secret   *json.RawMessage `json:"secret"`
	Approval *json.RawMessage `json:"approval"`
}

func (a *api) putSecret(ctx context.Context, method, projectID, env string, loc location, value string) error {
	var res writeResult
	err := a.do(ctx, method, "/v4/secrets/"+url.PathEscape(loc.Key), nil, writeSecret{
		ProjectID:   projectID,
		Environment: env,
		SecretPath:  loc.Dir,
		SecretValue: value,
		Type:        typeShared,
	}, &res)
	if err != nil {
		return err
	}
	if res.Secret == nil && res.Approval != nil {
		return fmt.Errorf("%s: the change was submitted for approval because the environment is protected by a change policy; approve it in Infisical", loc)
	}
	return nil
}

func (a *api) createSecret(ctx context.Context, projectID, env string, loc location, value string) error {
	return a.putSecret(ctx, http.MethodPost, projectID, env, loc, value)
}

func (a *api) updateSecret(ctx context.Context, projectID, env string, loc location, value string) error {
	return a.putSecret(ctx, http.MethodPatch, projectID, env, loc, value)
}

// createFolder creates dir, including missing parents. An existing folder is
// not an error.
func (a *api) createFolder(ctx context.Context, projectID, env, dir string) error {
	parent, name := "/", strings.Trim(dir, "/")
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		parent, name = "/"+name[:i], name[i+1:]
	}
	err := a.do(ctx, http.MethodPost, "/v2/folders", nil, map[string]string{
		"projectId":   projectID,
		"environment": env,
		"name":        name,
		"path":        parent,
	}, nil)
	var e *apiError
	if errors.As(err, &e) && e.Status == http.StatusBadRequest && strings.Contains(e.Message, "already exists") {
		return nil
	}
	return err
}
