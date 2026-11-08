// Package bitwarden implements the vault-sync backend on top of the official
// Bitwarden Password Manager CLI (bw).
//
// Storage layout: each project is a vault folder named after the project.
// Each file is either a Secure Note named after the file's logical path (the
// note body holds the contents) or an attachment (see resolve for the exact
// matching rules). Authentication, encryption and session handling are left
// entirely to bw.
package bitwarden

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

// Type is the identifier used in secrets.yaml.
const Type = "bitwarden"

// maxNoteSize is the largest plaintext or encoded Secure Note body. Bitwarden caps
// encrypted notes at 10,000 characters, and encryption inflates the plaintext
// by roughly a third, so larger files are stored as attachments instead.
const maxNoteSize = 5000

// Encoding is metadata rather than a body prefix so existing notes stay literal.
const noteEncodingField = "vault-sync.encoding"

// Backend is the Bitwarden implementation of backend.Backend.
type Backend struct {
	client      *client
	account     string
	log         *slog.Logger
	interactive func() bool

	authenticated bool
	folders       map[string]folder // by project
	items         map[string][]item // by folder id
}

var (
	_ backend.Backend        = (*Backend)(nil)
	_ backend.AccountManager = (*Backend)(nil)
)

// New is the backend.Factory for Bitwarden.
func New(cfg backend.Config) (backend.Backend, error) {
	if cfg.Environment != "" {
		return nil, fmt.Errorf("backend.environment is not supported by the %s backend; remove it from secrets.yaml", Type)
	}
	if cfg.Server != "" {
		return nil, fmt.Errorf("backend.server is not supported by the %s backend; remove it from secrets.yaml and select the server with `bw config server <url>`", Type)
	}
	return newWithRunner(cfg, newExecRunner()), nil
}

func newWithRunner(cfg backend.Config, r runner) *Backend {
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Backend{
		client:      &client{run: r, session: os.Getenv(sessionEnv)},
		account:     cfg.Account,
		log:         log.With("backend", Type),
		interactive: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		folders:     map[string]folder{},
		items:       map[string][]item{},
	}
}

// Name implements backend.Backend.
func (b *Backend) Name() string { return "Bitwarden" }

// Close implements backend.Backend. The session key only ever lives in memory.
func (b *Backend) Close() error {
	b.client.session = ""
	return nil
}

// Authenticate checks the bw login state, logs in and unlocks the vault
// interactively when needed, verifies the configured account and syncs the
// local cache.
func (b *Backend) Authenticate(ctx context.Context) error {
	if b.authenticated {
		return nil
	}
	st, err := b.client.status(ctx)
	if err != nil {
		return err
	}
	b.log.Debug("bw status", "status", st.Status, "server", st.ServerURL)

	if st.Status == "unauthenticated" {
		if err := b.login(ctx); err != nil {
			return err
		}
		if st, err = b.client.status(ctx); err != nil {
			return err
		}
	}
	if err := checkAccount(b.account, st.UserEmail); err != nil {
		return err
	}

	if st.Status == "locked" {
		if b.client.session != "" {
			return fmt.Errorf("%w: the Bitwarden session in %s is invalid or expired; run `export %s=$(bw unlock --raw)` or unset it to unlock interactively",
				backend.ErrAuthentication, sessionEnv, sessionEnv)
		}
		if !b.interactive() {
			return fmt.Errorf("%w: the Bitwarden vault is locked; run `export %s=$(bw unlock --raw)` first",
				backend.ErrAuthentication, sessionEnv)
		}
		fmt.Fprintln(os.Stderr, "Bitwarden vault is locked.")
		key, err := b.client.unlock(ctx)
		if err != nil {
			return fmt.Errorf("%w: %w", backend.ErrAuthentication, err)
		}
		b.client.session = key
	} else if st.Status != "unlocked" {
		return fmt.Errorf("%w: unexpected Bitwarden status %q", backend.ErrAuthentication, st.Status)
	}

	if err := b.client.sync(ctx); err != nil {
		b.log.Warn("bw sync failed; using the local vault cache", "error", err)
	}
	b.authenticated = true
	return nil
}

// login runs an interactive `bw login` and keeps the resulting session key in
// memory. The configured account is pre-filled when it is a full email.
func (b *Backend) login(ctx context.Context) error {
	if !b.interactive() {
		return fmt.Errorf("%w: not logged in to Bitwarden; run `vault-sync login` or `bw login` in a terminal%s",
			backend.ErrAuthentication, accountHint(b.account))
	}
	fmt.Fprintf(os.Stderr, "Not logged in to Bitwarden%s.\n", accountHint(b.account))
	email := ""
	if strings.Contains(b.account, "@") {
		email = b.account
	}
	key, err := b.client.login(ctx, email)
	if err != nil {
		return fmt.Errorf("%w: %w", backend.ErrAuthentication, err)
	}
	b.client.session = key
	return nil
}

// Status implements backend.AccountManager.
func (b *Backend) Status(ctx context.Context) (backend.AccountStatus, error) {
	st, err := b.client.status(ctx)
	if err != nil {
		return backend.AccountStatus{}, err
	}
	if st.Status == "unauthenticated" {
		return backend.AccountStatus{Server: st.ServerURL}, nil
	}
	return backend.AccountStatus{
		LoggedIn:        true,
		Locked:          st.Status != "unlocked",
		User:            st.UserEmail,
		Server:          st.ServerURL,
		AccountMismatch: checkAccount(b.account, st.UserEmail) != nil,
	}, nil
}

// Login implements backend.AccountManager. It does nothing when a user is
// already logged in.
func (b *Backend) Login(ctx context.Context) (backend.AccountStatus, error) {
	st, err := b.Status(ctx)
	if err != nil || st.LoggedIn {
		return st, err
	}
	if err := b.login(ctx); err != nil {
		return st, err
	}
	return b.Status(ctx)
}

// Logout implements backend.AccountManager.
func (b *Backend) Logout(ctx context.Context) error {
	if err := b.client.logout(ctx); err != nil {
		return err
	}
	b.client.session = ""
	b.authenticated = false
	return nil
}

func accountHint(account string) string {
	if account == "" {
		return ""
	}
	return " (expected account: " + account + ")"
}

// checkAccount ensures the logged-in user matches the configured account. The
// account may be the full email address or its local part.
func checkAccount(account, email string) error {
	if account == "" || email == "" {
		return nil
	}
	local, _, _ := strings.Cut(email, "@")
	if strings.EqualFold(account, email) || (!strings.Contains(account, "@") && strings.EqualFold(account, local)) {
		return nil
	}
	return fmt.Errorf("%w: logged in to Bitwarden as %s but secrets.yaml expects account %q; run `vault-sync logout` and log in again with the right account",
		backend.ErrAuthentication, email, account)
}

// ResolveFile implements backend.Backend. Attachments are downloaded lazily.
func (b *Backend) ResolveFile(ctx context.Context, project, filename string) (io.ReadCloser, error) {
	if err := b.Authenticate(ctx); err != nil {
		return nil, err
	}
	f, ok, err := b.folder(ctx, project, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, backend.NotFoundError(project, filename, fmt.Sprintf("no Bitwarden folder named %q", project))
	}
	items, err := b.listItems(ctx, f.ID)
	if err != nil {
		return nil, err
	}
	m, err := single(project, filename, items)
	if err != nil {
		return nil, err
	}
	b.log.Debug("resolved resource", "file", filename, "representation", m.repr.String(), "item", m.item.ID)

	switch m.repr {
	case reprNote:
		notes := ""
		if m.item.Notes != nil {
			notes = *m.item.Notes
		}
		for _, field := range m.item.Fields {
			if field.Name != noteEncodingField {
				continue
			}
			if field.Value != "base64" {
				return nil, fmt.Errorf("unsupported Secure Note encoding; upgrade vault-sync or correct the %s custom field in Bitwarden", noteEncodingField)
			}
			content, err := base64.StdEncoding.Strict().DecodeString(notes)
			if err != nil {
				return nil, fmt.Errorf("invalid Base64 in Secure Note; restore the note from a backup or push the original file again")
			}
			return io.NopCloser(bytes.NewReader(content)), nil
		}
		return io.NopCloser(strings.NewReader(notes)), nil
	default:
		return &lazyReader{fetch: func() ([]byte, error) {
			return b.client.getAttachment(ctx, m.item.ID, m.attachment.ID)
		}}, nil
	}
}

func single(project, filename string, items []item) (match, error) {
	matches := resolve(items, filename)
	switch len(matches) {
	case 0:
		detail := ""
		if ids := namedItems(items, filename); len(ids) > 0 {
			detail = fmt.Sprintf("item(s) %v exist but are neither a Secure Note nor carry an attachment named %q", ids, path.Base(filename))
		}
		return match{}, backend.NotFoundError(project, filename, detail)
	case 1:
		return matches[0], nil
	default:
		descs := make([]string, len(matches))
		for i, m := range matches {
			descs[i] = m.describe()
		}
		return match{}, backend.AmbiguousError(project, filename, descs)
	}
}

// StoreFile implements backend.Backend. New files become Secure Notes when they
// hold small text or Base64-encoded binary data, and attachments otherwise.
// Existing resources keep their representation unless a note grows too large.
func (b *Backend) StoreFile(ctx context.Context, project, filename string, content []byte) error {
	if err := b.Authenticate(ctx); err != nil {
		return err
	}
	f, _, err := b.folder(ctx, project, true)
	if err != nil {
		return err
	}
	items, err := b.listItems(ctx, f.ID)
	if err != nil {
		return err
	}
	defer delete(b.items, f.ID) // the folder content changes below

	matches := resolve(items, filename)
	if len(matches) > 1 {
		_, err := single(project, filename, items)
		return err
	}
	notes, encoded, fits := encodeNote(content)

	if len(matches) == 0 {
		doc := newSecureNote(filename, f.ID, "")
		if fits {
			doc["notes"] = notes
			setNoteEncoding(doc, encoded)
		}
		b.log.Debug("creating secure note", "file", filename, "attachment", !fits)
		it, err := b.client.createItem(ctx, doc)
		if err != nil {
			return err
		}
		if !fits {
			if err := b.client.createAttachment(ctx, it.ID, path.Base(filename), content); err != nil {
				// Do not leave an empty note behind: it would resolve as an
				// empty file on the next pull.
				if derr := b.client.deleteItem(ctx, it.ID); derr != nil {
					b.log.Warn("could not remove empty item after failed upload", "file", filename, "id", it.ID)
				}
				return err
			}
		}
		return nil
	}

	m := matches[0]
	switch m.repr {
	case reprNote:
		if !fits {
			b.log.Debug("note too large; converting to attachment", "file", filename)
			if err := b.client.createAttachment(ctx, m.item.ID, path.Base(filename), content); err != nil {
				return err
			}
		}
		doc, err := b.client.getItemRaw(ctx, m.item.ID)
		if err != nil {
			return err
		}
		if fits {
			doc["notes"] = notes
		} else {
			doc["notes"] = nil
		}
		setNoteEncoding(doc, fits && encoded)
		return b.client.editItem(ctx, m.item.ID, doc)
	default:
		name := m.attachment.FileName
		if strings.Contains(name, "/") {
			return fmt.Errorf("cannot replace attachment %q: names containing '/' cannot be uploaded by the Bitwarden CLI; rename it to %q", name, path.Base(name))
		}
		// Upload the replacement first so a failure never loses the old copy.
		if err := b.client.createAttachment(ctx, m.item.ID, name, content); err != nil {
			return err
		}
		return b.client.deleteAttachment(ctx, m.item.ID, m.attachment.ID)
	}
}

func newSecureNote(name, folderID, notes string) map[string]any {
	return map[string]any{
		"type":       itemTypeSecureNote,
		"name":       name,
		"folderId":   folderID,
		"notes":      notes,
		"secureNote": map[string]any{"type": 0},
		"favorite":   false,
		"reprompt":   0,
	}
}

// encodeNote keeps text readable and uses Base64 for binary (including NULs).
// The limit applies to the stored body, including Base64 expansion.
func encodeNote(content []byte) (notes string, encoded, fits bool) {
	if len(content) > maxNoteSize {
		return "", false, false
	}
	if utf8.Valid(content) && !bytes.ContainsRune(content, 0) {
		return string(content), false, true
	}
	if base64.StdEncoding.EncodedLen(len(content)) > maxNoteSize {
		return "", false, false
	}
	return base64.StdEncoding.EncodeToString(content), true, true
}

// setNoteEncoding preserves unrelated custom fields from the raw item document.
func setNoteEncoding(doc map[string]any, encoded bool) {
	fields, _ := doc["fields"].([]any)
	kept := make([]any, 0, len(fields)+1)
	for _, field := range fields {
		if f, ok := field.(map[string]any); ok && f["name"] == noteEncodingField {
			continue
		}
		kept = append(kept, field)
	}
	if encoded {
		kept = append(kept, map[string]any{"name": noteEncodingField, "value": "base64", "type": 0})
	}
	doc["fields"] = kept
}

// folder returns the folder named exactly project. Folder search in bw is
// fuzzy, so results are filtered by exact name.
func (b *Backend) folder(ctx context.Context, project string, create bool) (folder, bool, error) {
	if f, ok := b.folders[project]; ok {
		return f, true, nil
	}
	found, err := b.client.listFolders(ctx, project)
	if err != nil {
		return folder{}, false, err
	}
	var exact []folder
	for _, f := range found {
		if f.Name == project && f.ID != "" {
			exact = append(exact, f)
		}
	}
	switch len(exact) {
	case 1:
		b.folders[project] = exact[0]
		return exact[0], true, nil
	case 0:
		if !create {
			return folder{}, false, nil
		}
		b.log.Debug("creating folder", "project", project)
		f, err := b.client.createFolder(ctx, project)
		if err != nil {
			return folder{}, false, err
		}
		b.folders[project] = f
		return f, true, nil
	default:
		return folder{}, false, fmt.Errorf("%w: %d Bitwarden folders are named %q; rename the duplicates", backend.ErrAmbiguous, len(exact), project)
	}
}

func (b *Backend) listItems(ctx context.Context, folderID string) ([]item, error) {
	if items, ok := b.items[folderID]; ok {
		return items, nil
	}
	items, err := b.client.listItems(ctx, folderID)
	if err != nil {
		return nil, err
	}
	// Defensive: only ever consider items that really live in this folder.
	kept := items[:0]
	for _, it := range items {
		if it.FolderID == folderID {
			kept = append(kept, it)
		}
	}
	b.items[folderID] = kept
	return kept, nil
}
