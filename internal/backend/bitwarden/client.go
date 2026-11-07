package bitwarden

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jordigarciaventura/vault-sync/internal/toolinstall"
)

// BinaryEnv overrides the path of the Bitwarden CLI executable.
const BinaryEnv = "VAULT_SYNC_BW_PATH"

// sessionEnv is the variable the Bitwarden CLI reads its session key from.
const sessionEnv = "BW_SESSION"

// runner executes the Bitwarden CLI. It exists so tests can replace the real
// binary with a fake.
type runner interface {
	// Run executes bw with args, feeding stdin, and returns stdout. The
	// returned error may include stderr, never stdout (which can contain
	// secrets).
	Run(ctx context.Context, env []string, stdin []byte, args ...string) ([]byte, error)
	// RunInteractive executes bw attached to the user's terminal (stdin and
	// stderr) and returns stdout.
	RunInteractive(ctx context.Context, env []string, args ...string) ([]byte, error)
}

// ErrCLINotFound is returned when the Bitwarden CLI cannot be executed.
var ErrCLINotFound = errors.New("the Bitwarden CLI (bw) could not be executed; install it with `brew install bitwarden-cli` or set " + BinaryEnv)

// Tool describes the Bitwarden CLI so it can be installed on first use.
var Tool = toolinstall.Tool{
	Name:        "Bitwarden CLI",
	Binary:      "bw",
	PathEnv:     BinaryEnv,
	BrewFormula: "bitwarden-cli",
	Repo:        "bitwarden/clients",
	TagPrefix:   "cli-v",
	Asset: func(version, goos, goarch string) (string, bool) {
		platform := map[string]string{"darwin": "macos", "linux": "linux"}[goos]
		switch {
		case platform == "" || (goarch != "amd64" && goarch != "arm64"):
			return "", false
		case goarch == "arm64":
			platform += "-arm64"
		}
		return fmt.Sprintf("bw-%s-%s.zip", platform, version), true
	},
	ManualInstall: "`brew install bitwarden-cli`",
}

// ErrPremiumRequired is returned when Bitwarden refuses to store an attachment
// because the account has no Premium or organization plan.
var ErrPremiumRequired = fmt.Errorf("attachments require a Bitwarden Premium or organization plan; only UTF-8 text files up to %d bytes can be stored as Secure Notes", maxNoteSize)

// execRunner runs the real bw. The executable is located (and installed if
// missing) on first use, so commands that never reach Bitwarden do not
// trigger an installation.
type execRunner struct {
	find func(context.Context) (string, error)
	path string
}

func newExecRunner() *execRunner {
	return &execRunner{find: func(ctx context.Context) (string, error) { return toolinstall.Find(ctx, Tool) }}
}

func (r *execRunner) command(ctx context.Context, env []string, args []string) (*exec.Cmd, error) {
	if r.path == "" {
		p, err := r.find(ctx)
		if err != nil {
			return nil, err
		}
		r.path = p
	}
	cmd := exec.CommandContext(ctx, r.path, args...)
	cmd.Env = append(os.Environ(), env...)
	// Keep bw's own output machine friendly.
	cmd.Env = append(cmd.Env, "BW_NOINTERACTION=true")
	return cmd, nil
}

func (r *execRunner) Run(ctx context.Context, env []string, stdin []byte, args ...string) ([]byte, error) {
	cmd, err := r.command(ctx, env, args)
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		return nil, cliError(args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func (r *execRunner) RunInteractive(ctx context.Context, env []string, args ...string) ([]byte, error) {
	cmd, err := r.command(ctx, env, args)
	if err != nil {
		return nil, err
	}
	// Interactive prompts must be allowed here.
	cmd.Env = append(cmd.Env, "BW_NOINTERACTION=false")
	var stdout bytes.Buffer
	cmd.Stdin = os.Stdin
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, cliError(args, err, "")
	}
	return stdout.Bytes(), nil
}

func cliError(args []string, err error, stderr string) error {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return ErrCLINotFound
	}
	verb := "bw"
	if len(args) > 0 {
		verb += " " + args[0]
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			verb += " " + args[1]
		}
	}
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("%s: %w", verb, err)
	}
	// bw error messages are short; keep only the first line.
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return fmt.Errorf("%s: %s", verb, msg)
}

// client is a thin, typed wrapper around the Bitwarden CLI.
type client struct {
	run     runner
	session string
}

func (c *client) env() []string {
	if c.session == "" {
		return nil
	}
	return []string{sessionEnv + "=" + c.session}
}

func (c *client) json(ctx context.Context, v any, args ...string) error {
	out, err := c.run.Run(ctx, c.env(), nil, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		// Do not include the output: it may contain secrets.
		return fmt.Errorf("bw %s: unexpected output format", strings.Join(args[:min(2, len(args))], " "))
	}
	return nil
}

type status struct {
	Status    string `json:"status"`
	UserEmail string `json:"userEmail"`
	ServerURL string `json:"serverUrl"`
}

func (c *client) status(ctx context.Context) (status, error) {
	var s status
	err := c.json(ctx, &s, "status")
	return s, err
}

func (c *client) unlock(ctx context.Context) (string, error) {
	out, err := c.run.RunInteractive(ctx, nil, "unlock", "--raw")
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", errors.New("bw unlock returned no session key")
	}
	return key, nil
}

// login runs `bw login` attached to the terminal so bw itself prompts for the
// email, master password and two-step code. email may be empty. It returns
// the session key of the freshly unlocked vault.
func (c *client) login(ctx context.Context, email string) (string, error) {
	args := []string{"login"}
	if email != "" {
		args = append(args, email)
	}
	args = append(args, "--raw")
	out, err := c.run.RunInteractive(ctx, nil, args...)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", errors.New("bw login returned no session key")
	}
	return key, nil
}

func (c *client) logout(ctx context.Context) error {
	_, err := c.run.Run(ctx, nil, nil, "logout")
	return err
}

func (c *client) sync(ctx context.Context) error {
	_, err := c.run.Run(ctx, c.env(), nil, "sync")
	return err
}

type folder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *client) listFolders(ctx context.Context, search string) ([]folder, error) {
	var fs []folder
	err := c.json(ctx, &fs, "list", "folders", "--search", search)
	return fs, err
}

func (c *client) createFolder(ctx context.Context, name string) (folder, error) {
	var f folder
	out, err := c.run.Run(ctx, c.env(), encode(map[string]string{"name": name}), "create", "folder")
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(out, &f); err != nil || f.ID == "" {
		return f, errors.New("bw create folder: unexpected output format")
	}
	return f, nil
}

// Item types as defined by Bitwarden.
const (
	itemTypeSecureNote = 2
)

type attachment struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	Size     string `json:"size"`
}

type item struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Type        int          `json:"type"`
	FolderID    string       `json:"folderId"`
	Notes       *string      `json:"notes"`
	Attachments []attachment `json:"attachments"`
}

func (c *client) listItems(ctx context.Context, folderID string) ([]item, error) {
	var items []item
	err := c.json(ctx, &items, "list", "items", "--folderid", folderID)
	return items, err
}

// getItemRaw returns the full JSON document of an item, preserving fields this
// package does not model so edits do not drop them.
func (c *client) getItemRaw(ctx context.Context, id string) (map[string]any, error) {
	var m map[string]any
	err := c.json(ctx, &m, "get", "item", id)
	return m, err
}

func (c *client) createItem(ctx context.Context, doc map[string]any) (item, error) {
	var it item
	out, err := c.run.Run(ctx, c.env(), encode(doc), "create", "item")
	if err != nil {
		return it, err
	}
	if err := json.Unmarshal(out, &it); err != nil || it.ID == "" {
		return it, errors.New("bw create item: unexpected output format")
	}
	return it, nil
}

func (c *client) editItem(ctx context.Context, id string, doc map[string]any) error {
	_, err := c.run.Run(ctx, c.env(), encode(doc), "edit", "item", id)
	return err
}

// getAttachment downloads an attachment into a private temporary directory,
// reads it into memory and removes the temporary copy.
func (c *client) getAttachment(ctx context.Context, itemID, attachmentID string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "vault-sync-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	target := filepath.Join(dir, "attachment")
	if _, err := c.run.Run(ctx, c.env(), nil, "get", "attachment", attachmentID, "--itemid", itemID, "--output", target); err != nil {
		return nil, err
	}
	return os.ReadFile(target)
}

// createAttachment uploads content as an attachment named fileName.
func (c *client) createAttachment(ctx context.Context, itemID, fileName string, content []byte) error {
	dir, err := os.MkdirTemp("", "vault-sync-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	_, err = c.run.Run(ctx, c.env(), nil, "create", "attachment", "--file", path, "--itemid", itemID)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "premium") {
		return fmt.Errorf("upload %q: %w", fileName, ErrPremiumRequired)
	}
	return err
}

// deleteItem permanently deletes an item (skipping the trash).
func (c *client) deleteItem(ctx context.Context, id string) error {
	_, err := c.run.Run(ctx, c.env(), nil, "delete", "item", id, "--permanent")
	return err
}

func (c *client) deleteAttachment(ctx context.Context, itemID, attachmentID string) error {
	_, err := c.run.Run(ctx, c.env(), nil, "delete", "attachment", attachmentID, "--itemid", itemID)
	return err
}

// encode produces the base64-encoded JSON the Bitwarden CLI expects. It is
// passed on stdin rather than as an argument so secrets never show up in the
// process list.
func encode(v any) []byte {
	raw, _ := json.Marshal(v)
	out := make([]byte, base64.StdEncoding.EncodedLen(len(raw)))
	base64.StdEncoding.Encode(out, raw)
	return out
}

// lazyReader defers a download until the first Read.
type lazyReader struct {
	fetch func() ([]byte, error)
	r     io.Reader
	err   error
}

func (l *lazyReader) Read(p []byte) (int, error) {
	if l.r == nil && l.err == nil {
		data, err := l.fetch()
		if err != nil {
			l.err = err
		} else {
			l.r = bytes.NewReader(data)
		}
	}
	if l.err != nil {
		return 0, l.err
	}
	return l.r.Read(p)
}

func (l *lazyReader) Close() error { return nil }
