package r2

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunner(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-wrangler")
	body := `#!/bin/sh
[ "$PWD" != "$R2_TEST_PROJECT" ] || exit 1
[ "$1" = login ] && exit 0
[ "$1 $2 $3 $4" = 'auth token --json ' ] || exit 1
printf '%s' '{"type":"oauth","token":"test-token"}'
`
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VAULT_SYNC_WRANGLER_PATH", script)
	t.Setenv("R2_TEST_PROJECT", dir)
	run := newRunner()
	out, err := run(context.Background(), false, "auth", "token", "--json")
	if err != nil || !strings.Contains(string(out), "test-token") {
		t.Fatalf("runner failed: %v", err)
	}
	if _, err := run(context.Background(), true, "login"); err != nil {
		t.Fatalf("interactive runner: %v", err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho SECRET\necho SECRET >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err = run(context.Background(), false, "auth", "token", "--json")
	if err == nil || len(out) != 0 || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("runner leaked credentials")
	}
}
