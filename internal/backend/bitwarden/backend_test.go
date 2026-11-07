package bitwarden

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

// fakeVault emulates the subset of the bw CLI used by the backend.
type fakeVault struct {
	status      string
	email       string
	folders     []folder
	items       []item
	attachments map[string][]byte // attachment id -> content
	nextID      int
	calls       [][]string
	unlocked    bool
	failOn      string // "verb noun" that returns an error
	failMsg     string // error text for failOn (default "simulated failure")
}

func newFakeVault() *fakeVault {
	return &fakeVault{status: "unlocked", email: "jordigarciaventura@example.com", attachments: map[string][]byte{}}
}

func (v *fakeVault) id() string { v.nextID++; return fmt.Sprintf("id-%d", v.nextID) }

func (v *fakeVault) addFolder(name string) string {
	f := folder{ID: v.id(), Name: name}
	v.folders = append(v.folders, f)
	return f.ID
}

func (v *fakeVault) addNote(folderID, name, notes string) *item {
	n := notes
	v.items = append(v.items, item{ID: v.id(), Name: name, Type: itemTypeSecureNote, FolderID: folderID, Notes: &n})
	return &v.items[len(v.items)-1]
}

func (v *fakeVault) addAttachment(itemID, fileName string, content []byte) {
	for i := range v.items {
		if v.items[i].ID == itemID {
			a := attachment{ID: v.id(), FileName: fileName}
			v.items[i].Attachments = append(v.items[i].Attachments, a)
			v.attachments[a.ID] = content
		}
	}
}

func (v *fakeVault) find(id string) *item {
	for i := range v.items {
		if v.items[i].ID == id {
			return &v.items[i]
		}
	}
	return nil
}

func flag(args []string, name string) string {
	i := slices.Index(args, name)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

func decode(stdin []byte, v any) error {
	raw, err := base64.StdEncoding.DecodeString(string(stdin))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (v *fakeVault) RunInteractive(_ context.Context, _ []string, args ...string) ([]byte, error) {
	v.calls = append(v.calls, args)
	switch args[0] {
	case "unlock":
		v.unlocked = true
		v.status = "unlocked"
		return []byte("session-key\n"), nil
	case "login":
		if v.status != "unauthenticated" {
			return nil, errors.New("You are already logged in")
		}
		v.status = "unlocked"
		return []byte("login-session-key\n"), nil
	}
	return nil, errors.New("unexpected interactive command")
}

func (v *fakeVault) Run(_ context.Context, env []string, stdin []byte, args ...string) ([]byte, error) {
	v.calls = append(v.calls, args)
	verb := args[0]
	noun := ""
	if len(args) > 1 {
		noun = args[1]
	}
	if v.failOn != "" && v.failOn == verb+" "+noun {
		msg := v.failMsg
		if msg == "" {
			msg = "simulated failure"
		}
		return nil, fmt.Errorf("bw %s %s: %s", verb, noun, msg)
	}
	if verb == "logout" {
		if v.status == "unauthenticated" {
			return nil, errors.New("You are not logged in.")
		}
		v.status = "unauthenticated"
		return []byte("You have logged out."), nil
	}
	if verb != "status" && v.status != "unlocked" {
		return nil, errors.New("vault is locked")
	}
	switch verb + " " + noun {
	case "status ":
		return json.Marshal(status{Status: v.status, UserEmail: v.email})
	case "sync ":
		return []byte("Syncing complete."), nil
	case "list folders":
		search := flag(args, "--search")
		var out []folder
		for _, f := range v.folders {
			if strings.Contains(strings.ToLower(f.Name), strings.ToLower(search)) {
				out = append(out, f)
			}
		}
		return json.Marshal(out)
	case "create folder":
		var f folder
		if err := decode(stdin, &f); err != nil {
			return nil, err
		}
		f.ID = v.id()
		v.folders = append(v.folders, f)
		return json.Marshal(f)
	case "list items":
		out := []item{}
		for _, it := range v.items {
			if it.FolderID == flag(args, "--folderid") {
				out = append(out, it)
			}
		}
		return json.Marshal(out)
	case "get item":
		it := v.find(args[2])
		if it == nil {
			return nil, errors.New("Not found.")
		}
		return json.Marshal(it)
	case "create item":
		var it item
		if err := decode(stdin, &it); err != nil {
			return nil, err
		}
		it.ID = v.id()
		v.items = append(v.items, it)
		return json.Marshal(it)
	case "edit item":
		var it item
		if err := decode(stdin, &it); err != nil {
			return nil, err
		}
		existing := v.find(args[2])
		existing.Notes = it.Notes
		return json.Marshal(existing)
	case "get attachment":
		data, ok := v.attachments[args[2]]
		if !ok {
			return nil, errors.New("Attachment not found.")
		}
		return nil, os.WriteFile(flag(args, "--output"), data, 0o600)
	case "create attachment":
		data, err := os.ReadFile(flag(args, "--file"))
		if err != nil {
			return nil, err
		}
		v.addAttachment(flag(args, "--itemid"), filepath.Base(flag(args, "--file")), data)
		return []byte("{}"), nil
	case "delete item":
		v.items = slices.DeleteFunc(v.items, func(it item) bool { return it.ID == args[2] })
		return nil, nil
	case "delete attachment":
		it := v.find(flag(args, "--itemid"))
		it.Attachments = slices.DeleteFunc(it.Attachments, func(a attachment) bool { return a.ID == args[2] })
		delete(v.attachments, args[2])
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected command %v", args)
}

func newTestBackend(t *testing.T, v *fakeVault, account string) *Backend {
	t.Helper()
	t.Setenv(sessionEnv, "")
	b := newWithRunner(backend.Config{Type: Type, Account: account}, v)
	b.interactive = func() bool { return true }
	return b
}

func read(t *testing.T, b *Backend, project, name string) (string, error) {
	t.Helper()
	rc, err := b.ResolveFile(context.Background(), project, name)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	return string(data), err
}

func TestResolveSecureNote(t *testing.T) {
	v := newFakeVault()
	fid := v.addFolder("Example")
	v.addNote(fid, "backend/.env", "A=1\n")
	v.addNote(v.addFolder("Example-old"), "backend/.env", "WRONG")

	got, err := read(t, newTestBackend(t, v, ""), "Example", "backend/.env")
	if err != nil || got != "A=1\n" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolveAttachmentOnNamedItem(t *testing.T) {
	v := newFakeVault()
	fid := v.addFolder("P")
	it := v.addNote(fid, "shared/keys/firmware-signing.key", "description, not the file")
	v.addAttachment(it.ID, "firmware-signing.key", []byte{0, 1, 2})

	got, err := read(t, newTestBackend(t, v, ""), "P", "shared/keys/firmware-signing.key")
	if err != nil || got != "\x00\x01\x02" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolveAttachmentByFullName(t *testing.T) {
	v := newFakeVault()
	fid := v.addFolder("P")
	bundle := v.addNote(fid, "env files", "")
	v.addAttachment(bundle.ID, ".env", []byte("ROOT=1"))

	got, err := read(t, newTestBackend(t, v, ""), "P", ".env")
	if err != nil || got != "ROOT=1" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolveAttachmentIsLazy(t *testing.T) {
	v := newFakeVault()
	it := v.addNote(v.addFolder("P"), "f", "")
	v.addAttachment(it.ID, "f", []byte("x"))
	b := newTestBackend(t, v, "")

	rc, err := b.ResolveFile(context.Background(), "P", "f")
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	for _, c := range v.calls {
		if c[0] == "get" && c[1] == "attachment" {
			t.Fatal("attachment downloaded before Read")
		}
	}
}

func TestResolveAmbiguous(t *testing.T) {
	v := newFakeVault()
	fid := v.addFolder("P")
	v.addNote(fid, "a/.env", "1")
	v.addNote(fid, "a/.env", "2")

	_, err := read(t, newTestBackend(t, v, ""), "P", "a/.env")
	if !errors.Is(err, backend.ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "1") && strings.Contains(err.Error(), `"2"`) {
		t.Error("error message leaks note contents")
	}
}

func TestResolveAmbiguousAcrossRepresentations(t *testing.T) {
	v := newFakeVault()
	fid := v.addFolder("P")
	v.addNote(fid, ".env", "note")
	other := v.addNote(fid, "bundle", "")
	v.addAttachment(other.ID, ".env", []byte("att"))

	if _, err := read(t, newTestBackend(t, v, ""), "P", ".env"); !errors.Is(err, backend.ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveAmbiguousFolders(t *testing.T) {
	v := newFakeVault()
	v.addFolder("P")
	v.addFolder("P")
	if _, err := read(t, newTestBackend(t, v, ""), "P", "x"); !errors.Is(err, backend.ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveNotFound(t *testing.T) {
	v := newFakeVault()
	fid := v.addFolder("P")
	v.items = append(v.items, item{ID: "login-1", Name: "x", Type: 1, FolderID: fid})

	b := newTestBackend(t, v, "")
	if _, err := read(t, b, "Missing", "x"); !errors.Is(err, backend.ErrNotFound) {
		t.Errorf("missing folder: %v", err)
	}
	_, err := read(t, b, "P", "x")
	if !errors.Is(err, backend.ErrNotFound) || !strings.Contains(err.Error(), "login-1") {
		t.Errorf("unusable item: %v", err)
	}
}

func TestAuthenticateStates(t *testing.T) {
	t.Run("unauthenticated interactive logs in", func(t *testing.T) {
		v := newFakeVault()
		v.status = "unauthenticated"
		b := newTestBackend(t, v, "jordigarciaventura@example.com")
		if err := b.Authenticate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if b.client.session != "login-session-key" {
			t.Errorf("session = %q", b.client.session)
		}
		want := []string{"login", "jordigarciaventura@example.com", "--raw"}
		if !slices.ContainsFunc(v.calls, func(c []string) bool { return slices.Equal(c, want) }) {
			t.Errorf("calls = %v", v.calls)
		}
	})
	t.Run("unauthenticated with local-part account omits email", func(t *testing.T) {
		v := newFakeVault()
		v.status = "unauthenticated"
		if err := newTestBackend(t, v, "jordigarciaventura").Authenticate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(v.calls, func(c []string) bool { return slices.Equal(c, []string{"login", "--raw"}) }) {
			t.Errorf("calls = %v", v.calls)
		}
	})
	t.Run("unauthenticated non-interactive", func(t *testing.T) {
		v := newFakeVault()
		v.status = "unauthenticated"
		b := newTestBackend(t, v, "")
		b.interactive = func() bool { return false }
		err := b.Authenticate(context.Background())
		if !errors.Is(err, backend.ErrAuthentication) || !strings.Contains(err.Error(), "vault-sync login") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("login as wrong account", func(t *testing.T) {
		v := newFakeVault()
		v.status = "unauthenticated"
		err := newTestBackend(t, v, "someone-else").Authenticate(context.Background())
		if !errors.Is(err, backend.ErrAuthentication) || !strings.Contains(err.Error(), "someone-else") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("locked interactive unlocks", func(t *testing.T) {
		v := newFakeVault()
		v.status = "locked"
		b := newTestBackend(t, v, "")
		if err := b.Authenticate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !v.unlocked || b.client.session != "session-key" {
			t.Error("vault was not unlocked")
		}
	})
	t.Run("locked non-interactive", func(t *testing.T) {
		v := newFakeVault()
		v.status = "locked"
		b := newTestBackend(t, v, "")
		b.interactive = func() bool { return false }
		err := b.Authenticate(context.Background())
		if !errors.Is(err, backend.ErrAuthentication) || !strings.Contains(err.Error(), "BW_SESSION") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("expired session", func(t *testing.T) {
		v := newFakeVault()
		v.status = "locked"
		b := newTestBackend(t, v, "")
		b.client.session = "stale"
		err := b.Authenticate(context.Background())
		if !errors.Is(err, backend.ErrAuthentication) || !strings.Contains(err.Error(), "expired") {
			t.Errorf("err = %v", err)
		}
		if v.unlocked {
			t.Error("should not prompt when BW_SESSION is set")
		}
	})
	t.Run("account mismatch", func(t *testing.T) {
		v := newFakeVault()
		err := newTestBackend(t, v, "someone-else").Authenticate(context.Background())
		if !errors.Is(err, backend.ErrAuthentication) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("account matches local part and full email", func(t *testing.T) {
		for _, acc := range []string{"jordigarciaventura", "JordiGarciaVentura@example.com"} {
			if err := newTestBackend(t, newFakeVault(), acc).Authenticate(context.Background()); err != nil {
				t.Errorf("%s: %v", acc, err)
			}
		}
	})
	t.Run("sync failure is not fatal", func(t *testing.T) {
		v := newFakeVault()
		v.failOn = "sync "
		if err := newTestBackend(t, v, "").Authenticate(context.Background()); err != nil {
			t.Errorf("err = %v", err)
		}
	})
}

func TestStoreCreatesFolderAndNote(t *testing.T) {
	v := newFakeVault()
	b := newTestBackend(t, v, "")
	if err := b.StoreFile(context.Background(), "New", "backend/.env", []byte("A=1")); err != nil {
		t.Fatal(err)
	}
	if len(v.folders) != 1 || v.folders[0].Name != "New" {
		t.Fatalf("folders = %+v", v.folders)
	}
	got, err := read(t, b, "New", "backend/.env")
	if err != nil || got != "A=1" {
		t.Fatalf("roundtrip got %q, %v", got, err)
	}
	if v.items[0].Type != itemTypeSecureNote || len(v.items[0].Attachments) != 0 {
		t.Errorf("expected plain secure note, got %+v", v.items[0])
	}
}

func TestStoreBinaryCreatesAttachment(t *testing.T) {
	v := newFakeVault()
	b := newTestBackend(t, v, "")
	content := []byte{0xff, 0x00, 0x10}
	if err := b.StoreFile(context.Background(), "P", "keys/a.key", content); err != nil {
		t.Fatal(err)
	}
	if len(v.items) != 1 || len(v.items[0].Attachments) != 1 || v.items[0].Attachments[0].FileName != "a.key" {
		t.Fatalf("items = %+v", v.items)
	}
	got, err := read(t, b, "P", "keys/a.key")
	if err != nil || got != string(content) {
		t.Fatalf("roundtrip got %q, %v", got, err)
	}
}

func TestStoreUpdatesExistingNote(t *testing.T) {
	v := newFakeVault()
	v.addNote(v.addFolder("P"), ".env", "old")
	b := newTestBackend(t, v, "")
	if err := b.StoreFile(context.Background(), "P", ".env", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if len(v.items) != 1 || *v.items[0].Notes != "new" {
		t.Fatalf("items = %+v", v.items)
	}
}

func TestStoreLargeNoteConvertsToAttachment(t *testing.T) {
	v := newFakeVault()
	v.addNote(v.addFolder("P"), "big.txt", "small")
	b := newTestBackend(t, v, "")
	big := strings.Repeat("x", maxNoteSize+1)
	if err := b.StoreFile(context.Background(), "P", "big.txt", []byte(big)); err != nil {
		t.Fatal(err)
	}
	got, err := read(t, b, "P", "big.txt")
	if err != nil || got != big {
		t.Fatalf("roundtrip len %d, %v", len(got), err)
	}
}

func TestStoreReplacesAttachment(t *testing.T) {
	v := newFakeVault()
	it := v.addNote(v.addFolder("P"), "k", "")
	v.addAttachment(it.ID, "k", []byte("old"))
	b := newTestBackend(t, v, "")
	if err := b.StoreFile(context.Background(), "P", "k", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if n := len(v.find(it.ID).Attachments); n != 1 {
		t.Fatalf("attachments = %d", n)
	}
	if got, _ := read(t, b, "P", "k"); got != "new" {
		t.Errorf("got %q", got)
	}
}

func TestStoreAmbiguous(t *testing.T) {
	v := newFakeVault()
	fid := v.addFolder("P")
	v.addNote(fid, "x", "1")
	v.addNote(fid, "x", "2")
	if err := newTestBackend(t, v, "").StoreFile(context.Background(), "P", "x", []byte("y")); !errors.Is(err, backend.ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretsNeverPassedAsArguments(t *testing.T) {
	v := newFakeVault()
	b := newTestBackend(t, v, "")
	secret := "SUPER_SECRET_VALUE"
	if err := b.StoreFile(context.Background(), "P", "f", []byte(secret)); err != nil {
		t.Fatal(err)
	}
	for _, c := range v.calls {
		for _, a := range c {
			if strings.Contains(a, secret) || strings.Contains(a, base64.StdEncoding.EncodeToString([]byte(secret))[:8]) {
				t.Fatalf("secret passed on the command line: %v", c)
			}
		}
	}
}

func TestEncode(t *testing.T) {
	var got map[string]string
	if err := decode(encode(map[string]string{"name": "x"}), &got); err != nil || got["name"] != "x" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestCLIError(t *testing.T) {
	err := cliError([]string{"get", "item", "secret-id"}, errors.New("exit status 1"), "Not found.\nstack trace")
	if err.Error() != "bw get item: Not found." {
		t.Errorf("err = %q", err)
	}
}

func TestStoreAttachmentWithoutPremiumLeavesNoItem(t *testing.T) {
	v := newFakeVault()
	v.addFolder("P")
	v.failOn = "create attachment"
	v.failMsg = "Premium status is required to use this feature."
	b := newTestBackend(t, v, "")

	err := b.StoreFile(context.Background(), "P", "big.bin", []byte{0, 1, 2})
	if !errors.Is(err, ErrPremiumRequired) {
		t.Fatalf("want ErrPremiumRequired, got %v", err)
	}
	if len(v.items) != 0 {
		t.Fatalf("failed upload left %d item(s) behind", len(v.items))
	}
}

func TestStoreAttachmentOtherFailureIsNotPremium(t *testing.T) {
	v := newFakeVault()
	v.addFolder("P")
	v.failOn = "create attachment"
	err := newTestBackend(t, v, "").StoreFile(context.Background(), "P", "big.bin", []byte{0})
	if err == nil || errors.Is(err, ErrPremiumRequired) {
		t.Fatalf("got %v", err)
	}
	if len(v.items) != 0 {
		t.Fatalf("failed upload left %d item(s) behind", len(v.items))
	}
}

func TestAccountManager(t *testing.T) {
	ctx := context.Background()

	t.Run("status logged out", func(t *testing.T) {
		v := newFakeVault()
		v.status = "unauthenticated"
		st, err := newTestBackend(t, v, "").Status(ctx)
		if err != nil || st.LoggedIn {
			t.Errorf("st = %+v, err = %v", st, err)
		}
	})
	t.Run("status locked with mismatch", func(t *testing.T) {
		v := newFakeVault()
		v.status = "locked"
		st, err := newTestBackend(t, v, "someone-else").Status(ctx)
		if err != nil || !st.LoggedIn || !st.Locked || !st.AccountMismatch || st.User != v.email {
			t.Errorf("st = %+v, err = %v", st, err)
		}
	})
	t.Run("login when logged out", func(t *testing.T) {
		v := newFakeVault()
		v.status = "unauthenticated"
		st, err := newTestBackend(t, v, "").Login(ctx)
		if err != nil || !st.LoggedIn || st.Locked || st.User != v.email {
			t.Errorf("st = %+v, err = %v", st, err)
		}
	})
	t.Run("login when logged in is a no-op", func(t *testing.T) {
		v := newFakeVault()
		v.status = "locked"
		st, err := newTestBackend(t, v, "").Login(ctx)
		if err != nil || !st.LoggedIn {
			t.Errorf("st = %+v, err = %v", st, err)
		}
		for _, c := range v.calls {
			if c[0] == "login" || c[0] == "unlock" {
				t.Errorf("unexpected call %v", c)
			}
		}
	})
	t.Run("logout", func(t *testing.T) {
		v := newFakeVault()
		b := newTestBackend(t, v, "")
		b.client.session = "key"
		if err := b.Logout(ctx); err != nil {
			t.Fatal(err)
		}
		if v.status != "unauthenticated" || b.client.session != "" {
			t.Errorf("status = %s, session = %q", v.status, b.client.session)
		}
	})
}

func TestNewRejectsEnvironment(t *testing.T) {
	_, err := New(backend.Config{Type: Type, Environment: "prod"})
	if err == nil || !strings.Contains(err.Error(), "backend.environment") {
		t.Errorf("err = %v", err)
	}
}

func TestNewRejectsServer(t *testing.T) {
	_, err := New(backend.Config{Type: Type, Server: "https://vault.example.com"})
	if err == nil || !strings.Contains(err.Error(), "bw config server") {
		t.Errorf("err = %v", err)
	}
}

func TestToolAssets(t *testing.T) {
	for _, tc := range []struct{ goos, goarch, want string }{
		{"darwin", "amd64", "bw-macos-2026.10.1.zip"},
		{"darwin", "arm64", "bw-macos-arm64-2026.10.1.zip"},
		{"linux", "amd64", "bw-linux-2026.10.1.zip"},
		{"linux", "arm64", "bw-linux-arm64-2026.10.1.zip"},
		{"linux", "386", ""},
		{"freebsd", "amd64", ""},
	} {
		got, ok := Tool.Asset("2026.10.1", tc.goos, tc.goarch)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("%s/%s: %q, %v", tc.goos, tc.goarch, got, ok)
		}
	}
}
