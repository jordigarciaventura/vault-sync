package infisical

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

const (
	projectSlug = "example-a1b2"
	projectName = "Example"
	projectUUID = "6f1c2b9e-0d4a-4c1e-9a8b-3f2e1d0c9b8a"
	userToken   = "user-token"
)

// fakeAPI emulates the subset of the Infisical REST API used by the backend.
type fakeAPI struct {
	mu       sync.Mutex
	token    string
	envs     []string
	folders  map[string]bool   // env + dir
	secrets  map[string]string // env + dir + key -> value
	requests []string          // "METHOD path?query"
	// approval makes writes return a pending approval instead of a secret.
	approval bool
	// failWith makes every request fail with this status.
	failWith int
	// extraProjects are listed next to the example project.
	extraProjects []map[string]any
	// noExample hides the example project, as if it did not exist yet.
	noExample bool
	// created records the bodies of project creations.
	created []map[string]any
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		token:   userToken,
		envs:    []string{"dev", "staging", "prod"},
		folders: map[string]bool{"dev\x00/": true, "prod\x00/": true, "staging\x00/": true},
		secrets: map[string]string{},
	}
}

func (f *fakeAPI) put(env, dir, key, value string) {
	f.folders[env+"\x00"+dir] = true
	f.secrets[env+"\x00"+dir+"\x00"+key] = value
}

func (f *fakeAPI) get(env, dir, key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.secrets[env+"\x00"+dir+"\x00"+key]
	return v, ok
}

func (f *fakeAPI) project() map[string]any {
	envs := []map[string]string{}
	for _, e := range f.envs {
		envs = append(envs, map[string]string{"slug": e, "name": e, "id": "env-" + e})
	}
	return map[string]any{"id": projectUUID, "name": projectName, "slug": projectSlug, "environments": envs}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"statusCode": status, "message": msg})
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())

	if f.failWith != 0 {
		fail(w, f.failWith, "simulated failure")
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		fail(w, http.StatusUnauthorized, "Token missing or invalid")
		return
	}
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/projects/slug/"):
		slug := strings.TrimPrefix(r.URL.Path, "/api/v1/projects/slug/")
		if !isSlug(slug) {
			// Like Infisical: a schema validation failure lists the issues.
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"statusCode": 422, "error": "ValidationFailure",
				"message": []map[string]any{{"code": "custom", "path": []string{"slug"}, "message": "Invalid slug"}},
			})
			return
		}
		if slug != projectSlug || f.noExample {
			fail(w, http.StatusNotFound, "Project not found")
			return
		}
		writeJSON(w, http.StatusOK, f.project())

	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["type"] != "secret-manager" {
			fail(w, http.StatusBadRequest, "bad body")
			return
		}
		f.created = append(f.created, body)
		p := f.project()
		p["name"] = body["projectName"]
		if slug, ok := body["slug"].(string); ok {
			p["slug"] = slug
		} else {
			p["slug"] = "generated-x1y2"
		}
		writeJSON(w, http.StatusOK, map[string]any{"project": p})

	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects":
		if q.Get("type") != "secret-manager" {
			fail(w, http.StatusBadRequest, "unexpected query")
			return
		}
		projects := f.extraProjects
		if !f.noExample {
			projects = append([]map[string]any{f.project()}, projects...)
		}
		writeJSON(w, http.StatusOK, map[string]any{"projects": projects})

	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/secrets":
		if q.Get("projectId") != projectUUID || q.Get("expandSecretReferences") != "false" || q.Get("includeImports") != "false" {
			fail(w, http.StatusBadRequest, "unexpected query")
			return
		}
		env, dir := q.Get("environment"), q.Get("secretPath")
		if !f.folders[env+"\x00"+dir] {
			fail(w, http.StatusNotFound, fmt.Sprintf("Folder with path '%s' not found", dir))
			return
		}
		out := []map[string]any{}
		prefix := env + "\x00" + dir + "\x00"
		for k, v := range f.secrets {
			if key, ok := strings.CutPrefix(k, prefix); ok {
				out = append(out, map[string]any{"id": "sec-" + key, "secretKey": key, "secretValue": v, "type": "shared", "secretValueHidden": false})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"secrets": out})

	case (r.Method == http.MethodPost || r.Method == http.MethodPatch) && strings.HasPrefix(r.URL.Path, "/api/v4/secrets/"):
		key := strings.TrimPrefix(r.URL.Path, "/api/v4/secrets/")
		var body writeSecret
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ProjectID != projectUUID || body.Type != "shared" {
			fail(w, http.StatusBadRequest, "bad body")
			return
		}
		if !f.folders[body.Environment+"\x00"+body.SecretPath] {
			fail(w, http.StatusNotFound, "Folder not found")
			return
		}
		id := body.Environment + "\x00" + body.SecretPath + "\x00" + key
		_, exists := f.secrets[id]
		if r.Method == http.MethodPost && exists {
			fail(w, http.StatusBadRequest, "Secret already exist")
			return
		}
		if r.Method == http.MethodPatch && !exists {
			fail(w, http.StatusNotFound, "Secret not found")
			return
		}
		if f.approval {
			writeJSON(w, http.StatusOK, map[string]any{"approval": map[string]any{"id": "appr-1"}})
			return
		}
		f.secrets[id] = serverNormalize(body.SecretValue)
		writeJSON(w, http.StatusOK, map[string]any{"secret": map[string]any{"id": "sec-" + key, "secretKey": key}})

	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/folders":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		parent := body["path"]
		full := strings.TrimSuffix(parent, "/") + "/" + body["name"]
		if f.folders[body["environment"]+"\x00"+full] {
			fail(w, http.StatusBadRequest, fmt.Sprintf("Folder with name '%s' already exists in path '%s'", body["name"], parent))
			return
		}
		// Like Infisical, create missing parents too.
		cur := ""
		for _, seg := range strings.Split(strings.Trim(full, "/"), "/") {
			cur += "/" + seg
			f.folders[body["environment"]+"\x00"+cur] = true
		}
		writeJSON(w, http.StatusOK, map[string]any{"folder": map[string]any{"id": "f", "name": body["name"], "path": full}})

	default:
		fail(w, http.StatusNotFound, "Route not found")
	}
}

// fakeCLI emulates the Infisical CLI commands used for authentication.
type fakeCLI struct {
	sessions []session
	calls    [][]string
	loginErr error
	// afterLogin is the session reported once `infisical login` succeeded.
	afterLogin session
	// statusDomain is the --domain passed to the last `login status`.
	statusDomain string
	// loginDomain is the --domain passed to the last `login`.
	loginDomain string
}

// withoutDomain strips a trailing "--domain <url>" from args.
func withoutDomain(args []string) ([]string, string) {
	if n := len(args); n >= 2 && args[n-2] == "--domain" {
		return args[:n-2], args[n-1]
	}
	return args, ""
}

func userSession(status string) session {
	return session{PrincipalType: principalUser, Status: status, Email: "jordi@example.com"}
}

func (c *fakeCLI) Run(_ context.Context, args ...string) ([]byte, error) {
	c.calls = append(c.calls, args)
	args, domain := withoutDomain(args)
	switch strings.Join(args, " ") {
	case "login status --json":
		c.statusDomain = domain
		out, _ := json.Marshal(map[string]any{"sessions": c.sessions})
		if len(c.sessions) == 0 || c.sessions[0].Status != stateAuthenticated {
			return out, errors.New("infisical login status: exit status 1")
		}
		return out, nil
	case "user get token --plain":
		return []byte(userToken + "\n"), nil
	case "logout":
		c.sessions = nil
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected command %v", args)
}

func (c *fakeCLI) RunInteractive(_ context.Context, args ...string) error {
	c.calls = append(c.calls, args)
	args, c.loginDomain = withoutDomain(args)
	if strings.Join(args, " ") != "login" {
		return fmt.Errorf("unexpected interactive command %v", args)
	}
	if c.loginErr != nil {
		return c.loginErr
	}
	c.sessions = []session{c.afterLogin}
	return nil
}

func (c *fakeCLI) called(cmd string) bool {
	return slices.ContainsFunc(c.calls, func(a []string) bool { return strings.Join(a, " ") == cmd })
}

type harness struct {
	b   *Backend
	api *fakeAPI
	cli *fakeCLI
	srv *httptest.Server
}

func newHarness(t *testing.T, cfg backend.Config) *harness {
	t.Helper()
	h := &harness{
		api: newFakeAPI(),
		cli: &fakeCLI{sessions: []session{userSession(stateAuthenticated)}, afterLogin: userSession(stateAuthenticated)},
	}
	h.srv = httptest.NewServer(h.api)
	t.Cleanup(h.srv.Close)
	for i := range h.cli.sessions {
		h.cli.sessions[i].Domain = h.srv.URL
	}
	h.cli.afterLogin.Domain = h.srv.URL
	h.b = newWithRunner(cfg, h.cli)
	h.b.interactive = func() bool { return false }
	h.b.getenv = func(string) string { return "" }
	return h
}

func read(t *testing.T, b *Backend, project, filename string) (string, error) {
	t.Helper()
	rc, err := b.ResolveFile(context.Background(), project, filename)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	return string(data), err
}

func TestResolveFile(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.api.put("dev", "/", ".env", "A=1\nB=${A}\n")
	h.api.put("dev", "/backend", ".env.production", "-----BEGIN KEY-----\nabc\n-----END KEY-----")
	h.api.put("prod", "/", ".env", "PROD=1\n")

	for _, tc := range []struct{ file, want string }{
		{".env", "A=1\nB=${A}\n"}, // references are not expanded
		{"backend/.env.production", "-----BEGIN KEY-----\nabc\n-----END KEY-----"},
	} {
		got, err := read(t, h.b, projectSlug, tc.file)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.file, got, err, tc.want)
		}
	}
	// One project lookup, one listing per folder.
	var lookups, lists int
	for _, r := range h.api.requests {
		switch {
		case strings.Contains(r, "/projects/slug/"):
			lookups++
		case strings.HasPrefix(r, "GET /api/v4/secrets?"):
			lists++
		}
	}
	if lookups != 1 || lists != 2 {
		t.Errorf("lookups = %d, lists = %d (%v)", lookups, lists, h.api.requests)
	}
}

func TestResolveFileEnvironment(t *testing.T) {
	h := newHarness(t, backend.Config{Environment: "prod"})
	h.api.put("dev", "/", ".env", "DEV=1\n")
	h.api.put("prod", "/", ".env", "PROD=1\n")
	if got, err := read(t, h.b, projectSlug, ".env"); err != nil || got != "PROD=1\n" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestResolveFileNotFound(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.api.put("dev", "/backend", "other", "x")
	for _, tc := range []struct{ file, detail string }{
		{"backend/.env", `no secret /backend/.env`},
		{"frontend/.env", `no folder "/frontend"`},
		{".env", `no secret /.env`},
	} {
		_, err := read(t, h.b, projectSlug, tc.file)
		if !errors.Is(err, backend.ErrNotFound) || !strings.Contains(err.Error(), tc.detail) {
			t.Errorf("%s: err = %v", tc.file, err)
		}
	}
}

func TestProjectResolution(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.api.put("dev", "/", ".env", "X=1\n")

	// A project id is used as-is, without a slug lookup.
	if _, err := read(t, h.b, projectUUID, ".env"); err != nil {
		t.Fatal(err)
	}
	for _, r := range h.api.requests {
		if strings.Contains(r, "/projects/slug/") {
			t.Errorf("unexpected slug lookup: %s", r)
		}
	}

	// A project name works too, including one that looks like a slug.
	for name, wantID := range map[string]string{projectName: projectUUID, "legacy": "id-legacy"} {
		h := newHarness(t, backend.Config{})
		h.api.extraProjects = []map[string]any{{"id": "id-legacy", "name": "legacy", "slug": "legacy-zz99"}}
		if err := h.b.Authenticate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if id, err := h.b.projectID(context.Background(), name); err != nil || id != wantID {
			t.Errorf("%s: id = %q, %v; want %q", name, id, err, wantID)
		}
	}

	_, err := read(t, h.b, "nope", ".env")
	if !errors.Is(err, backend.ErrNotFound) || !strings.Contains(err.Error(), `name or slug "nope"`) ||
		!strings.Contains(err.Error(), `"Example" (slug example-a1b2)`) {
		t.Errorf("err = %v", err)
	}

	h3 := newHarness(t, backend.Config{})
	h3.api.extraProjects = []map[string]any{{"id": "dup", "name": projectName, "slug": "example-c3d4"}}
	_, err = read(t, h3.b, projectName, ".env")
	if !errors.Is(err, backend.ErrAmbiguous) || !strings.Contains(err.Error(), "example-a1b2, example-c3d4") {
		t.Errorf("duplicate names: err = %v", err)
	}

	h2 := newHarness(t, backend.Config{Environment: "qa"})
	_, err = read(t, h2.b, projectSlug, ".env")
	if !errors.Is(err, backend.ErrNotFound) || !strings.Contains(err.Error(), "available: dev, staging, prod") {
		t.Errorf("err = %v", err)
	}
}

func TestStoreFileCreatesFolderAndSecret(t *testing.T) {
	h := newHarness(t, backend.Config{})
	ctx := context.Background()
	content := "SECRET=hunter2\nOTHER=x\n"
	if err := h.b.StoreFile(ctx, projectSlug, "apps/api/.env", []byte(content)); err != nil {
		t.Fatal(err)
	}
	if v, ok := h.api.get("dev", "/apps/api", ".env"); !ok || v != content {
		t.Errorf("stored %q, %v", v, ok)
	}
	// The secret value never travels in a URL.
	for _, r := range h.api.requests {
		if strings.Contains(r, "hunter2") {
			t.Errorf("secret in request line: %s", r)
		}
	}
	// Round trip through a fresh backend (no cache).
	h.b = newWithRunner(backend.Config{}, h.cli)
	if got, err := read(t, h.b, projectSlug, "apps/api/.env"); err != nil || got != content {
		t.Errorf("round trip: %q, %v", got, err)
	}
}

func TestStoreFileUpdatesExisting(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.api.put("dev", "/", "id_ed25519", "old")
	ctx := context.Background()

	if _, err := read(t, h.b, projectSlug, "id_ed25519"); err != nil { // warm the cache
		t.Fatal(err)
	}
	if err := h.b.StoreFile(ctx, projectSlug, "id_ed25519", []byte("new\n")); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.api.get("dev", "/", "id_ed25519"); v != "new\n" {
		t.Errorf("stored %q", v)
	}
	if !slices.ContainsFunc(h.api.requests, func(r string) bool { return strings.HasPrefix(r, "PATCH /api/v4/secrets/id_ed25519") }) {
		t.Errorf("expected an update, got %v", h.api.requests)
	}
	// The cache was invalidated: a read sees the new value.
	if got, err := read(t, h.b, projectSlug, "id_ed25519"); err != nil || got != "new\n" {
		t.Errorf("after push: %q, %v", got, err)
	}
}

func TestStoreFileExistingFolderNewSecret(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.api.put("dev", "/backend", "other", "x")
	if err := h.b.StoreFile(context.Background(), projectSlug, "backend/.env", []byte("A=1")); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.api.get("dev", "/backend", ".env"); v != "A=1" {
		t.Errorf("stored %q", v)
	}
	if slices.ContainsFunc(h.api.requests, func(r string) bool { return strings.Contains(r, "/v2/folders") }) {
		t.Error("folder should not be created again")
	}
}

func TestStoreFileRejectsLossyContent(t *testing.T) {
	h := newHarness(t, backend.Config{})
	for name, content := range map[string]string{
		"leading space":   "  A=1\n",
		"blank lines":     "A=1\n\n",
		"trailing spaces": "A=1  ",
		"binary":          "\xff\xfe\x00",
		"nul":             "A=\x00",
	} {
		err := h.b.StoreFile(context.Background(), projectSlug, ".env", []byte(content))
		if !errors.Is(err, errNotStorable) {
			t.Errorf("%s: err = %v", name, err)
		}
		if strings.Contains(fmt.Sprint(err), strings.TrimSpace(content)) && strings.TrimSpace(content) != "" {
			t.Errorf("%s: content leaked into error %v", name, err)
		}
	}
	if len(h.api.requests) != 0 || len(h.cli.calls) != 0 {
		t.Errorf("nothing should be contacted: %v %v", h.api.requests, h.cli.calls)
	}
}

func TestStoreFileApprovalRequired(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.api.approval = true
	err := h.b.StoreFile(context.Background(), projectSlug, ".env", []byte("A=1\n"))
	if err == nil || !strings.Contains(err.Error(), "approval") {
		t.Errorf("err = %v", err)
	}
}

func TestInvalidLayout(t *testing.T) {
	h := newHarness(t, backend.Config{})
	for _, file := range []string{".github/token", "my dir/.env", "a/b:c"} {
		if _, err := read(t, h.b, projectSlug, file); err == nil {
			t.Errorf("%s: expected an error", file)
		}
		if err := h.b.StoreFile(context.Background(), projectSlug, file, []byte("x")); err == nil {
			t.Errorf("%s: expected an error", file)
		}
	}
}

func TestAPIErrors(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.api.token = "something-else"
	_, err := read(t, h.b, projectUUID, ".env")
	if !errors.Is(err, backend.ErrAuthentication) {
		t.Errorf("401: err = %v", err)
	}

	h = newHarness(t, backend.Config{})
	h.api.failWith = http.StatusInternalServerError
	_, err = read(t, h.b, projectUUID, ".env")
	if err == nil || !strings.Contains(err.Error(), "simulated failure") || !strings.Contains(err.Error(), "500") {
		t.Errorf("500: err = %v", err)
	}
}

func TestValidationErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"statusCode": 422, "error": "ValidationFailure",
			"message": []map[string]any{
				{"code": "too_small", "path": []any{"secretValue"}, "message": "Required", "received": "s3cret"},
				{"code": "custom", "path": []any{"tags", 0}, "message": "Invalid"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	err := newAPI(srv.URL, "t").do(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	want := "infisical api: secretValue: Required; tags.0: Invalid (HTTP 422)"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v; want %s", err, want)
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Error("the error echoes request input")
	}
}

func TestAuthenticateNotLoggedIn(t *testing.T) {
	h := newHarness(t, backend.Config{Account: "jordi"})
	h.cli.sessions = nil
	_, err := read(t, h.b, projectSlug, ".env")
	if !errors.Is(err, backend.ErrAuthentication) || !strings.Contains(err.Error(), "vault-sync login") || !strings.Contains(err.Error(), "jordi") {
		t.Errorf("err = %v", err)
	}
	if h.cli.called("login") {
		t.Error("must not prompt without a terminal")
	}

	h.b.interactive = func() bool { return true }
	h.api.put("dev", "/", ".env", "A=1")
	if got, err := read(t, h.b, projectSlug, ".env"); err != nil || got != "A=1" {
		t.Errorf("after login: %q, %v", got, err)
	}
	if !h.cli.called("login") {
		t.Error("expected interactive login")
	}
}

func TestAuthenticateExpired(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.cli.sessions = []session{userSession(stateExpired)}
	h.cli.sessions[0].Domain = h.srv.URL
	h.b.interactive = func() bool { return true }
	h.api.put("dev", "/", ".env", "A=1")
	if _, err := read(t, h.b, projectSlug, ".env"); err != nil {
		t.Fatal(err)
	}
	if !h.cli.called("login") {
		t.Error("expected a renewed login")
	}
}

func TestAuthenticateMachineIdentity(t *testing.T) {
	h := newHarness(t, backend.Config{Account: "ci-bot"})
	h.cli.sessions = []session{{
		PrincipalType: "machine-identity", Status: stateAuthenticated, Domain: h.srv.URL,
		Identity: &struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{ID: "id-1", Name: "ci-bot"},
	}}
	h.api.token = "mi-token"
	h.b.getenv = func(k string) string {
		if k == tokenEnv {
			return "mi-token"
		}
		return ""
	}
	h.api.put("dev", "/", ".env", "A=1")
	if _, err := read(t, h.b, projectSlug, ".env"); err != nil {
		t.Fatal(err)
	}
	if h.cli.called("user get token --plain") {
		t.Error("user token must not be used with a machine identity")
	}
	if err := h.b.Logout(context.Background()); err == nil || !strings.Contains(err.Error(), tokenEnv) {
		t.Errorf("logout: %v", err)
	}
}

func TestAccountMismatch(t *testing.T) {
	for account, ok := range map[string]bool{"jordi": true, "JORDI@example.com": true, "someone": false, "jordi@other.com": false} {
		h := newHarness(t, backend.Config{Account: account})
		h.api.put("dev", "/", ".env", "A=1")
		_, err := read(t, h.b, projectSlug, ".env")
		if ok != (err == nil) {
			t.Errorf("%s: err = %v", account, err)
		}
		if !ok && !errors.Is(err, backend.ErrAuthentication) {
			t.Errorf("%s: err = %v", account, err)
		}
	}
}

func TestAccountManager(t *testing.T) {
	h := newHarness(t, backend.Config{Account: "other"})
	ctx := context.Background()
	st, err := h.b.Status(ctx)
	if err != nil || !st.LoggedIn || st.User != "jordi@example.com" || st.Server != h.srv.URL || !st.AccountMismatch {
		t.Errorf("status = %+v, %v", st, err)
	}

	if err := h.b.Logout(ctx); err != nil || !h.cli.called("logout") {
		t.Fatalf("logout: %v", err)
	}
	if st, _ := h.b.Status(ctx); st.LoggedIn {
		t.Error("still logged in")
	}

	h.b.interactive = func() bool { return true }
	if st, err := h.b.Login(ctx); err != nil || !st.LoggedIn {
		t.Errorf("login = %+v, %v", st, err)
	}

	// An expired session reads as logged out so `vault-sync login` renews it.
	h.cli.sessions = []session{userSession(stateExpired)}
	if st, _ := h.b.Status(ctx); st.LoggedIn {
		t.Error("expired session reported as logged in")
	}
}

func TestStatusOldCLI(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.b.cli.run = runnerFunc(func(args ...string) ([]byte, error) {
		return nil, cliError(args, errors.New("exit status 1"), `Error: unknown command "status" for "infisical login"`)
	})
	_, err := h.b.Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Errorf("err = %v", err)
	}
}

type runnerFunc func(args ...string) ([]byte, error)

func (f runnerFunc) Run(_ context.Context, args ...string) ([]byte, error) { return f(args...) }
func (f runnerFunc) RunInteractive(_ context.Context, args ...string) error {
	_, err := f(args...)
	return err
}

func TestToolAssets(t *testing.T) {
	if got, ok := Tool.Asset("0.45.4", "darwin", "arm64"); !ok || got != "cli_0.45.4_darwin_arm64.tar.gz" {
		t.Errorf("%q, %v", got, ok)
	}
	if _, ok := Tool.Asset("0.45.4", "windows", "amd64"); ok {
		t.Error("windows should be unsupported")
	}
}

func TestPushCreatesProject(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ ref, wantSlug string }{
		{"new-app", "new-app"},
		{"My App", ""}, // not a slug: the server picks one
		{"app", ""},    // too short for a project slug
	} {
		h := newHarness(t, backend.Config{})
		h.api.noExample = true
		if err := h.b.StoreFile(ctx, tc.ref, ".env", []byte("A=1\n")); err != nil {
			t.Errorf("%s: %v", tc.ref, err)
			continue
		}
		if len(h.api.created) != 1 || h.api.created[0]["projectName"] != tc.ref {
			t.Errorf("%s: created = %v", tc.ref, h.api.created)
			continue
		}
		if slug, _ := h.api.created[0]["slug"].(string); slug != tc.wantSlug {
			t.Errorf("%s: slug = %q, want %q", tc.ref, slug, tc.wantSlug)
		}
		if v, ok := h.api.get("dev", "/", ".env"); !ok || v != "A=1\n" {
			t.Errorf("%s: secret = %q, %v", tc.ref, v, ok)
		}
	}
}

func TestProjectNotCreated(t *testing.T) {
	ctx := context.Background()

	// pull never creates a project.
	h := newHarness(t, backend.Config{})
	h.api.noExample = true
	if _, err := read(t, h.b, "new-app", ".env"); !errors.Is(err, backend.ErrNotFound) || len(h.api.created) != 0 {
		t.Errorf("pull: err = %v, created = %v", err, h.api.created)
	}

	// A name differing only in case points at the existing project.
	h = newHarness(t, backend.Config{})
	err := h.b.StoreFile(ctx, "example", ".env", []byte("A=1\n"))
	if !errors.Is(err, backend.ErrNotFound) || !strings.Contains(err.Error(), `"Example" (slug example-a1b2)`) || len(h.api.created) != 0 {
		t.Errorf("case: err = %v, created = %v", err, h.api.created)
	}

	// A missing environment in the new project is reported.
	h = newHarness(t, backend.Config{Environment: "qa"})
	h.api.noExample = true
	if err := h.b.StoreFile(ctx, "new-app", ".env", []byte("A=1\n")); !errors.Is(err, backend.ErrNotFound) || !strings.Contains(err.Error(), `no environment "qa"`) {
		t.Errorf("env: err = %v", err)
	}
}

func TestNormalizeServer(t *testing.T) {
	for in, want := range map[string]string{
		"":                                   "",
		"secrets.example.com":                "https://secrets.example.com",
		"https://Secrets.Example.com/":       "https://secrets.example.com",
		"https://secrets.example.com/api":    "https://secrets.example.com",
		"https://secrets.example.com/api/":   "https://secrets.example.com",
		"http://localhost:8080":              "http://localhost:8080",
		"https://example.com/infisical/api/": "https://example.com/infisical",
	} {
		if got, err := normalizeServer(in); err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"ftp://x", "https://", "https://u:p@x", "https://x?a=1", "https://x/#f"} {
		if _, err := normalizeServer(in); err == nil {
			t.Errorf("%q: expected an error", in)
		}
		if _, err := New(backend.Config{Server: in}); err == nil {
			t.Errorf("New(%q): expected an error", in)
		}
	}
}

func TestServerMatches(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.b.server, _ = normalizeServer(h.srv.URL + "/api/")
	h.api.put("dev", "/", ".env", "A=1\n")
	if _, err := read(t, h.b, projectSlug, ".env"); err != nil {
		t.Fatal(err)
	}
	if h.cli.statusDomain != "" {
		t.Errorf("a user session must keep its own domain, got --domain %s", h.cli.statusDomain)
	}
}

func TestServerMismatch(t *testing.T) {
	ctx := context.Background()
	newMismatch := func() *harness {
		h := newHarness(t, backend.Config{})
		h.b.server, _ = normalizeServer(h.srv.URL)
		h.cli.sessions[0].Domain = "https://app.infisical.com"
		return h
	}

	// pull and push refuse to use a session on another server.
	h := newMismatch()
	_, err := read(t, h.b, projectSlug, ".env")
	if !errors.Is(err, backend.ErrAuthentication) || !strings.Contains(err.Error(), "expects server "+h.b.server) || !strings.Contains(err.Error(), "vault-sync login") {
		t.Errorf("err = %v", err)
	}
	if len(h.api.requests) != 0 {
		t.Errorf("API used despite the mismatch: %v", h.api.requests)
	}

	// Status reports it; Login switches to the configured server.
	h = newMismatch()
	st, err := h.b.Status(ctx)
	if err != nil || !st.LoggedIn || !st.ServerMismatch || st.Server != "https://app.infisical.com" {
		t.Errorf("status = %+v, %v", st, err)
	}
	h.b.interactive = func() bool { return true }
	st, err = h.b.Login(ctx)
	if err != nil || !st.LoggedIn || st.ServerMismatch || h.cli.loginDomain != h.b.server {
		t.Errorf("login = %+v, %v (--domain %q)", st, err, h.cli.loginDomain)
	}

	// Without a terminal, Login fails with a hint.
	h = newMismatch()
	if _, err := h.b.Login(ctx); !errors.Is(err, backend.ErrAuthentication) {
		t.Errorf("non-interactive login: %v", err)
	}

	// A first login goes to the configured server.
	h = newHarness(t, backend.Config{})
	h.b.server, _ = normalizeServer(h.srv.URL)
	h.cli.sessions = nil
	h.b.interactive = func() bool { return true }
	if err := h.b.Authenticate(ctx); err != nil || h.cli.loginDomain != h.b.server {
		t.Errorf("authenticate: %v (--domain %q)", err, h.cli.loginDomain)
	}
}

func TestServerMachineIdentity(t *testing.T) {
	h := newHarness(t, backend.Config{})
	h.b.server, _ = normalizeServer(h.srv.URL)
	h.cli.sessions = []session{{PrincipalType: "machine-identity", Status: stateAuthenticated, Domain: h.srv.URL}}
	h.api.token = "mi-token"
	h.b.getenv = func(k string) string {
		if k == tokenEnv {
			return "mi-token"
		}
		return ""
	}
	h.api.put("dev", "/", ".env", "A=1")
	if _, err := read(t, h.b, projectSlug, ".env"); err != nil {
		t.Fatal(err)
	}
	// The token is checked against the configured server.
	if h.cli.statusDomain != h.b.server {
		t.Errorf("status --domain = %q, want %q", h.cli.statusDomain, h.b.server)
	}
}
