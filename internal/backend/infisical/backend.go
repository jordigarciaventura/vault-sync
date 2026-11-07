// Package infisical implements the vault-sync backend on top of Infisical.
//
// Storage layout: project is an Infisical project (its slug or id) and
// backend.environment one of its environments. Each file is a shared secret:
// the directory part of the logical filename is the folder (secret path) and
// the base name the secret key, so "backend/.env" is the secret ".env" in
// folder "/backend". The file contents are the secret value.
//
// Authentication is left to the official Infisical CLI (`infisical login`, or
// a machine identity token in INFISICAL_TOKEN); secrets are read and written
// through the REST API so values never appear in process arguments and are
// stored byte for byte.
package infisical

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

// Type is the identifier used in secrets.yaml.
const Type = "infisical"

// DefaultEnvironment is used when backend.environment is not set. It matches
// the Infisical CLI's default.
const DefaultEnvironment = "dev"

// domainEnv is the Infisical CLI's variable for self-hosted or EU instances.
const domainEnv = "INFISICAL_DOMAIN"

const defaultDomain = "https://app.infisical.com"

// Backend is the Infisical implementation of backend.Backend.
type Backend struct {
	cli         *cli
	account     string
	env         string
	server      string // normalized backend.server; empty when not configured
	log         *slog.Logger
	interactive func() bool
	getenv      func(string) string
	newAPI      func(domain, token string) *api

	api        *api
	projectIDs map[string]string   // by project slug or id
	secrets    map[string][]secret // by projectID + env + dir
}

var (
	_ backend.Backend        = (*Backend)(nil)
	_ backend.AccountManager = (*Backend)(nil)
)

// New is the backend.Factory for Infisical.
func New(cfg backend.Config) (backend.Backend, error) {
	if _, err := normalizeServer(cfg.Server); err != nil {
		return nil, err
	}
	return newWithRunner(cfg, newExecRunner()), nil
}

// normalizeServer turns backend.server into the form the Infisical CLI
// reports (scheme and host, plus a path for instances served under one,
// without the /api suffix). "" stays "".
func normalizeServer(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	raw := s
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("backend.server: %q is not a valid Infisical URL; use e.g. https://infisical.example.com", raw)
	}
	path := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/api")
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimSuffix(path, "/"), nil
}

// sameServer reports whether the session domain d is the configured server.
func (b *Backend) sameServer(d string) bool {
	n, err := normalizeServer(d)
	return err == nil && n == b.server
}

func newWithRunner(cfg backend.Config, r runner) *Backend {
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	env := cfg.Environment
	if env == "" {
		env = DefaultEnvironment
	}
	server, _ := normalizeServer(cfg.Server) // validated by New
	return &Backend{
		cli:         &cli{run: r},
		account:     cfg.Account,
		env:         env,
		server:      server,
		log:         log.With("backend", Type),
		interactive: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		getenv:      os.Getenv,
		newAPI:      newAPI,
		projectIDs:  map[string]string{},
		secrets:     map[string][]secret{},
	}
}

// Name implements backend.Backend.
func (b *Backend) Name() string { return "Infisical" }

// Close implements backend.Backend. The access token only lives in memory.
func (b *Backend) Close() error {
	b.api = nil
	return nil
}

// Authenticate finds the active Infisical session, logs in interactively when
// needed, verifies the configured account and obtains an access token.
func (b *Backend) Authenticate(ctx context.Context) error {
	if b.api != nil {
		return nil
	}
	s, ok, err := b.status(ctx)
	if err != nil {
		return err
	}
	if ok {
		b.log.Debug("infisical session", "principal", s.PrincipalType, "status", s.Status, "domain", s.Domain)
	}
	if err := b.checkServer(s, ok); err != nil {
		return err
	}
	if !ok || (s.Status == stateExpired && s.PrincipalType == principalUser) {
		if err := b.login(ctx, ok); err != nil {
			return err
		}
		if s, ok, err = b.status(ctx); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: still not logged in to Infisical after `infisical login`", backend.ErrAuthentication)
		}
	}
	if s.Status != stateAuthenticated {
		return fmt.Errorf("%w: the Infisical session is %s; run `vault-sync login`%s", backend.ErrAuthentication, s.Status, tokenHint(s))
	}
	if err := checkAccount(b.account, s); err != nil {
		return err
	}

	var token string
	if s.PrincipalType == principalUser {
		if token, err = b.cli.userToken(ctx); err != nil {
			return fmt.Errorf("%w: %w", backend.ErrAuthentication, err)
		}
	} else {
		token = strings.TrimSpace(b.getenv(tokenEnv))
		if token == "" {
			return fmt.Errorf("%w: the Infisical CLI reports a %s session but %s is empty", backend.ErrAuthentication, s.PrincipalType, tokenEnv)
		}
	}
	b.api = b.newAPI(b.domain(s), token)
	return nil
}

// status asks the CLI for the active session. A machine identity token is
// checked against the configured server, as INFISICAL_DOMAIN would; a user
// session keeps the server it logged in to, so a mismatch can be detected.
func (b *Backend) status(ctx context.Context) (session, bool, error) {
	domain := ""
	if b.server != "" && strings.TrimSpace(b.getenv(tokenEnv)) != "" && b.getenv(domainEnv) == "" {
		domain = b.server
	}
	return b.cli.status(ctx, domain)
}

// serverMismatch reports that the session belongs to another instance than
// backend.server.
func (b *Backend) serverMismatch(s session, ok bool) bool {
	return ok && b.server != "" && !b.sameServer(b.sessionDomain(s))
}

func (b *Backend) checkServer(s session, ok bool) error {
	if !b.serverMismatch(s, ok) {
		return nil
	}
	if s.PrincipalType != principalUser {
		return fmt.Errorf("%w: %s is for %s but secrets.yaml expects server %s; set %s to %s or remove backend.server",
			backend.ErrAuthentication, tokenEnv, b.sessionDomain(s), b.server, domainEnv, b.server)
	}
	return fmt.Errorf("%w: logged in to Infisical at %s but secrets.yaml expects server %s; run `vault-sync login` to log in to it",
		backend.ErrAuthentication, b.sessionDomain(s), b.server)
}

// sessionDomain is the instance a session belongs to.
func (b *Backend) sessionDomain(s session) string {
	if s.Domain != "" {
		return s.Domain
	}
	if d := b.getenv(domainEnv); d != "" {
		return d
	}
	return defaultDomain
}

func (b *Backend) domain(s session) string {
	if b.server != "" {
		return b.server // checkServer made sure the session matches
	}
	if s.Domain != "" {
		return s.Domain
	}
	if d := b.getenv(domainEnv); d != "" {
		return d
	}
	return defaultDomain
}

// login runs an interactive `infisical login`. expired reports that a session
// exists but has to be renewed.
func (b *Backend) login(ctx context.Context, expired bool) error {
	what := "Not logged in to Infisical"
	if expired {
		what = "Your Infisical login has expired"
	}
	if !b.interactive() {
		return fmt.Errorf("%w: %s; run `vault-sync login` in a terminal or set %s to a machine identity token%s",
			backend.ErrAuthentication, strings.ToLower(what[:1])+what[1:], tokenEnv, accountHint(b.account))
	}
	fmt.Fprintf(os.Stderr, "%s%s.\n", what, accountHint(b.account))
	if err := b.cli.login(ctx, b.server); err != nil {
		return fmt.Errorf("%w: %w", backend.ErrAuthentication, err)
	}
	return nil
}

func tokenHint(s session) string {
	if s.PrincipalType != principalUser {
		return " or refresh " + tokenEnv
	}
	return ""
}

// Status implements backend.AccountManager. An expired user session counts as
// logged out, so `vault-sync login` renews it.
func (b *Backend) Status(ctx context.Context) (backend.AccountStatus, error) {
	s, ok, err := b.status(ctx)
	if err != nil || !ok || s.Status != stateAuthenticated {
		return backend.AccountStatus{}, err
	}
	return backend.AccountStatus{
		LoggedIn:        true,
		User:            s.user(),
		Server:          b.sessionDomain(s),
		AccountMismatch: checkAccount(b.account, s) != nil,
		ServerMismatch:  b.serverMismatch(s, ok),
	}, nil
}

// Login implements backend.AccountManager. It does nothing when a session to
// the configured server is already active; a user session to another server
// is replaced.
func (b *Backend) Login(ctx context.Context) (backend.AccountStatus, error) {
	st, err := b.Status(ctx)
	if err != nil || (st.LoggedIn && !st.ServerMismatch) {
		return st, err
	}
	s, exists, err := b.status(ctx)
	if err != nil {
		return st, err
	}
	if st.ServerMismatch && s.PrincipalType != principalUser {
		return st, b.checkServer(s, exists) // a token cannot be switched by logging in
	}
	if st.ServerMismatch {
		fmt.Fprintf(os.Stderr, "Logged in to Infisical at %s, but secrets.yaml uses %s.\n", st.Server, b.server)
		exists = false
	}
	// A session that exists but is not active has expired.
	if err := b.login(ctx, exists); err != nil {
		return st, err
	}
	return b.Status(ctx)
}

// Logout implements backend.AccountManager.
func (b *Backend) Logout(ctx context.Context) error {
	s, ok, err := b.status(ctx)
	if err != nil {
		return err
	}
	if ok && s.PrincipalType != principalUser {
		return fmt.Errorf("authenticated through %s; unset it to log out", tokenEnv)
	}
	if err := b.cli.logout(ctx); err != nil {
		return err
	}
	b.api = nil
	return nil
}

func accountHint(account string) string {
	if account == "" {
		return ""
	}
	return " (expected account: " + account + ")"
}

// checkAccount ensures the session matches the configured account: a user's
// email (or its local part), or a machine identity's name or id.
func checkAccount(account string, s session) error {
	if account == "" {
		return nil
	}
	var candidates []string
	if s.Email != "" {
		local, _, _ := strings.Cut(s.Email, "@")
		candidates = append(candidates, s.Email)
		if !strings.Contains(account, "@") {
			candidates = append(candidates, local)
		}
	}
	if s.Identity != nil {
		candidates = append(candidates, s.Identity.Name, s.Identity.ID)
	}
	if len(candidates) == 0 {
		return nil // the CLI did not tell us who is logged in
	}
	for _, c := range candidates {
		if c != "" && strings.EqualFold(account, c) {
			return nil
		}
	}
	return fmt.Errorf("%w: logged in to Infisical as %s but secrets.yaml expects account %q; run `vault-sync logout` and log in again with the right account",
		backend.ErrAuthentication, s.user(), account)
}

// ResolveFile implements backend.Backend.
func (b *Backend) ResolveFile(ctx context.Context, project, filename string) (io.ReadCloser, error) {
	loc, err := locate(filename)
	if err != nil {
		return nil, err
	}
	if err := b.Authenticate(ctx); err != nil {
		return nil, err
	}
	projectID, err := b.projectID(ctx, project)
	if err != nil {
		return nil, err
	}
	secrets, err := b.listSecrets(ctx, projectID, loc.Dir)
	if errors.Is(err, errFolderNotFound) {
		return nil, backend.NotFoundError(project, filename, fmt.Sprintf("no folder %q in Infisical environment %q", loc.Dir, b.env))
	}
	if err != nil {
		return nil, err
	}
	s, err := b.single(project, filename, loc, secrets)
	if err != nil {
		return nil, err
	}
	if s.SecretValueHidden {
		return nil, fmt.Errorf("%w: not allowed to read the value of secret %s in environment %q; ask for read access", backend.ErrAuthentication, loc, b.env)
	}
	b.log.Debug("resolved resource", "file", filename, "secret", loc.String(), "id", s.ID, "size", len(s.Value))
	return io.NopCloser(strings.NewReader(s.Value)), nil
}

func (b *Backend) single(project, filename string, loc location, secrets []secret) (secret, error) {
	var matches []secret
	for _, s := range secrets {
		if s.Key == loc.Key && s.Type == typeShared {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return secret{}, backend.NotFoundError(project, filename, fmt.Sprintf("no secret %s in Infisical environment %q", loc, b.env))
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = fmt.Sprintf("secret %s (%s)", loc, m.ID)
		}
		return secret{}, backend.AmbiguousError(project, filename, ids)
	}
}

// StoreFile implements backend.Backend. Missing folders are created.
func (b *Backend) StoreFile(ctx context.Context, project, filename string, content []byte) error {
	loc, err := locate(filename)
	if err != nil {
		return err
	}
	if err := checkStorable(filename, content); err != nil {
		return err
	}
	if err := b.Authenticate(ctx); err != nil {
		return err
	}
	projectID, err := b.projectID(ctx, project)
	if np := (*noProjectError)(nil); errors.As(err, &np) {
		projectID, err = b.createProject(ctx, project)
	}
	if err != nil {
		return err
	}
	secrets, err := b.listSecrets(ctx, projectID, loc.Dir)
	missingFolder := errors.Is(err, errFolderNotFound)
	if err != nil && !missingFolder {
		return err
	}
	defer delete(b.secrets, b.cacheKey(projectID, loc.Dir)) // the folder changes below

	if missingFolder {
		b.log.Debug("creating folder", "path", loc.Dir, "environment", b.env)
		if err := b.api.createFolder(ctx, projectID, b.env, loc.Dir); err != nil {
			return err
		}
	} else {
		existing, err := b.single(project, filename, loc, secrets)
		switch {
		case err == nil:
			b.log.Debug("updating secret", "file", filename, "secret", loc.String(), "id", existing.ID, "size", len(content))
			return b.api.updateSecret(ctx, projectID, b.env, loc, string(content))
		case !errors.Is(err, backend.ErrNotFound):
			return err
		}
	}
	b.log.Debug("creating secret", "file", filename, "secret", loc.String(), "size", len(content))
	return b.api.createSecret(ctx, projectID, b.env, loc, string(content))
}

// projectID resolves project, which may be a project id, slug or name, and
// checks that the configured environment exists.
func (b *Backend) projectID(ctx context.Context, project string) (string, error) {
	if id, ok := b.projectIDs[project]; ok {
		return id, nil
	}
	if looksLikeUUID(project) {
		// Ids work with every token type, including legacy service tokens
		// that cannot look projects up.
		b.projectIDs[project] = project
		return project, nil
	}
	p, err := b.findProject(ctx, project)
	if err != nil {
		return "", err
	}
	if err := b.checkEnvironment(project, p); err != nil {
		return "", err
	}
	b.log.Debug("resolved project", "project", project, "id", p.ID, "slug", p.Slug)
	b.projectIDs[project] = p.ID
	return p.ID, nil
}

// checkEnvironment makes sure the configured environment exists in p.
func (b *Backend) checkEnvironment(ref string, p project) error {
	envs := make([]string, len(p.Environments))
	for i, e := range p.Environments {
		envs[i] = e.Slug
	}
	if len(envs) > 0 && !slices.Contains(envs, b.env) {
		return fmt.Errorf("%w: Infisical project %q has no environment %q (available: %s); set backend.environment in secrets.yaml",
			backend.ErrNotFound, ref, b.env, strings.Join(envs, ", "))
	}
	return nil
}

// findProject looks ref up by slug and, failing that, by exact name
// among the accessible projects.
func (b *Backend) findProject(ctx context.Context, ref string) (project, error) {
	var slugErr error
	if isSlug(ref) {
		p, err := b.api.projectBySlug(ctx, ref)
		if err == nil {
			return p, nil
		}
		if !isStatus(err, http.StatusNotFound) {
			return p, err
		}
		slugErr = fmt.Errorf("%w: no Infisical project with slug %q is accessible; check `project` in secrets.yaml (it takes the project name, slug or id)", backend.ErrNotFound, ref)
	}

	all, err := b.api.listProjects(ctx)
	if err != nil {
		if slugErr != nil {
			return project{}, slugErr // e.g. a token that cannot list projects
		}
		return project{}, err
	}
	var matches []project
	for _, p := range all {
		if p.Name == ref {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		for _, p := range all {
			if strings.EqualFold(p.Name, ref) || strings.EqualFold(p.Slug, ref) {
				// Not created on push: it would be a near-duplicate.
				return project{}, fmt.Errorf("%w: no Infisical project has the name or slug %q, but %q (slug %s) differs only in case; set `project` in secrets.yaml to its exact name or slug",
					backend.ErrNotFound, ref, p.Name, p.Slug)
			}
		}
		known := make([]string, len(all))
		for i, p := range all {
			known[i] = fmt.Sprintf("%q (slug %s)", p.Name, p.Slug)
		}
		hint := "none"
		if len(known) > 0 {
			hint = strings.Join(known, ", ")
		}
		return project{}, &noProjectError{ref: ref, accessible: hint}
	default:
		slugs := make([]string, len(matches))
		for i, p := range matches {
			slugs[i] = p.Slug
		}
		return project{}, fmt.Errorf("%w: %d Infisical projects are named %q (slugs: %s); set `project` in secrets.yaml to one of the slugs",
			backend.ErrAmbiguous, len(matches), ref, strings.Join(slugs, ", "))
	}
}

// noProjectError reports that no accessible project has a name or slug. push
// creates the project in that case.
type noProjectError struct {
	ref        string
	accessible string
}

func (e *noProjectError) Error() string {
	return fmt.Sprintf("%v: no accessible Infisical project has the name or slug %q (accessible: %s); check `project` in secrets.yaml, or run `vault-sync push` to create it",
		backend.ErrNotFound, e.ref, e.accessible)
}

func (e *noProjectError) Unwrap() error { return backend.ErrNotFound }

// createProject creates the project named ref for a push. ref is also used as
// the slug when it can be one, so later lookups find it directly.
func (b *Backend) createProject(ctx context.Context, ref string) (string, error) {
	slug := ""
	if isSlug(ref) && len(ref) >= minCreateSlugLen && len(ref) <= 64 {
		slug = ref
	}
	p, err := b.api.createProject(ctx, ref, slug)
	if err != nil {
		return "", fmt.Errorf("create Infisical project %q: %w", ref, err)
	}
	fmt.Fprintf(os.Stderr, "Created Infisical project %q (slug %s).\n", p.Name, p.Slug)
	b.log.Debug("created project", "project", ref, "id", p.ID, "slug", p.Slug)
	if err := b.checkEnvironment(ref, p); err != nil {
		return "", err
	}
	b.projectIDs[ref] = p.ID
	return p.ID, nil
}

// minCreateSlugLen is the shortest slug Infisical accepts for a new project.
const minCreateSlugLen = 5

// isSlug reports whether s can be an Infisical slug: lower-case letters,
// digits and '-'. The server rejects anything else with HTTP 422.
func isSlug(s string) bool {
	return s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
}

func (b *Backend) cacheKey(projectID, dir string) string {
	return projectID + "\x00" + b.env + "\x00" + dir
}

func (b *Backend) listSecrets(ctx context.Context, projectID, dir string) ([]secret, error) {
	key := b.cacheKey(projectID, dir)
	if s, ok := b.secrets[key]; ok {
		return s, nil
	}
	s, err := b.api.listSecrets(ctx, projectID, b.env, dir)
	if err != nil {
		return nil, err
	}
	b.secrets[key] = s
	return s, nil
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}
