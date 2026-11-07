package infisical

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jordigarciaventura/vault-sync/internal/toolinstall"
)

// BinaryEnv overrides the path of the Infisical CLI executable.
const BinaryEnv = "VAULT_SYNC_INFISICAL_PATH"

// tokenEnv holds a machine identity or service token. The Infisical CLI gives
// it precedence over a logged-in user, and so does vault-sync.
const tokenEnv = "INFISICAL_TOKEN" //nolint:gosec // a variable name, not a credential

// ErrCLINotFound is returned when the Infisical CLI cannot be executed.
var ErrCLINotFound = errors.New("the Infisical CLI (infisical) could not be executed; install it with `brew install infisical` or set " + BinaryEnv)

// Tool describes the Infisical CLI so it can be installed on first use.
var Tool = toolinstall.Tool{
	Name:        "Infisical CLI",
	Binary:      "infisical",
	PathEnv:     BinaryEnv,
	BrewFormula: "infisical",
	Repo:        "Infisical/cli",
	Asset: func(version, goos, goarch string) (string, bool) {
		if (goos != "darwin" && goos != "linux") || (goarch != "amd64" && goarch != "arm64") {
			return "", false
		}
		return fmt.Sprintf("cli_%s_%s_%s.tar.gz", version, goos, goarch), true
	},
	ManualInstall: "`brew install infisical`",
}

// runner executes the Infisical CLI. It exists so tests can replace the real
// binary with a fake.
type runner interface {
	// Run executes infisical with args and returns stdout, which is returned
	// even when the command fails (some commands report state through their
	// exit code). The error may include stderr, never stdout (which can hold
	// tokens).
	Run(ctx context.Context, args ...string) ([]byte, error)
	// RunInteractive executes infisical attached to the user's terminal.
	RunInteractive(ctx context.Context, args ...string) error
}

// execRunner runs the real infisical. The executable is located (and
// installed if missing) on first use.
type execRunner struct {
	find func(context.Context) (string, error)
	path string
}

func newExecRunner() *execRunner {
	return &execRunner{find: func(ctx context.Context) (string, error) { return toolinstall.Find(ctx, Tool) }}
}

func (r *execRunner) resolve(ctx context.Context) (string, error) {
	if r.path == "" {
		p, err := r.find(ctx)
		if err != nil {
			return "", err
		}
		r.path = p
	}
	return r.path, nil
}

func (r *execRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	// --silent keeps tips and update notices out of machine-readable output.
	cmd := exec.CommandContext(ctx, bin, append(args, "--silent")...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), cliError(args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func (r *execRunner) RunInteractive(ctx context.Context, args ...string) error {
	bin, err := r.resolve(ctx)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr // keep vault-sync's stdout clean
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return cliError(args, err, "")
	}
	return nil
}

func cliError(args []string, err error, stderr string) error {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return ErrCLINotFound
	}
	verb := "infisical"
	for _, a := range args[:min(2, len(args))] {
		if strings.HasPrefix(a, "-") {
			break
		}
		verb += " " + a
	}
	msg := strings.TrimSpace(stderr)
	if strings.Contains(msg, "unknown command") || strings.Contains(msg, "unknown flag") {
		return fmt.Errorf("%s: your Infisical CLI is too old; upgrade it (e.g. `brew upgrade infisical`)", verb)
	}
	if msg == "" {
		return fmt.Errorf("%s: %w", verb, err)
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return fmt.Errorf("%s: %s", verb, msg)
}

// Session states reported by `infisical login status`.
const (
	stateAuthenticated = "authenticated"
	stateExpired       = "expired"
)

// Principal types reported by `infisical login status`.
const principalUser = "user"

type session struct {
	PrincipalType string `json:"principalType"`
	Status        string `json:"status"`
	Domain        string `json:"domain"`
	Email         string `json:"email"`
	TokenSource   string `json:"tokenSource"`
	Identity      *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"identity"`
}

// user identifies the session's principal for display and account checks.
func (s session) user() string {
	if s.Email != "" {
		return s.Email
	}
	if s.Identity != nil {
		if s.Identity.Name != "" {
			return s.Identity.Name
		}
		return s.Identity.ID
	}
	return ""
}

// cli is a thin, typed wrapper around the Infisical CLI. It is only used for
// authentication; secrets go through the REST API.
type cli struct {
	run runner
}

// status returns the active session, if any. The CLI prefers INFISICAL_TOKEN
// over a logged-in user and lists sessions in that order. A non-empty domain
// makes the CLI check the session against that instance.
func (c *cli) status(ctx context.Context, domain string) (session, bool, error) {
	args := []string{"login", "status", "--json"}
	if domain != "" {
		args = append(args, "--domain", domain)
	}
	out, runErr := c.run.Run(ctx, args...)
	var st struct {
		Sessions []session `json:"sessions"`
	}
	// The command exits non-zero when not logged in or expired but still
	// prints the JSON document.
	if err := json.Unmarshal(out, &st); err != nil {
		if runErr != nil {
			return session{}, false, runErr
		}
		return session{}, false, errors.New("infisical login status: unexpected output format")
	}
	if len(st.Sessions) == 0 {
		return session{}, false, nil
	}
	return st.Sessions[0], true, nil
}

// userToken returns the access token of the logged-in user. It is kept in
// memory only.
func (c *cli) userToken(ctx context.Context) (string, error) {
	out, err := c.run.Run(ctx, "user", "get", "token", "--plain")
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" || strings.ContainsAny(tok, " \n") {
		return "", errors.New("infisical user get token: unexpected output format")
	}
	return tok, nil
}

// login logs a user in interactively, to domain when it is not empty.
func (c *cli) login(ctx context.Context, domain string) error {
	if domain != "" {
		return c.run.RunInteractive(ctx, "login", "--domain", domain)
	}
	return c.run.RunInteractive(ctx, "login")
}

func (c *cli) logout(ctx context.Context) error {
	_, err := c.run.Run(ctx, "logout")
	return err
}
