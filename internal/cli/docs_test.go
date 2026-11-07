package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteMarkdownDocs(t *testing.T) {
	var first, second bytes.Buffer
	if err := WriteMarkdownDocs(&first); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarkdownDocs(&second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("output is not deterministic")
	}

	out := first.String()
	for _, want := range []string{
		"### `vault-sync pull`",
		"### `vault-sync push`",
		"### `vault-sync self-upgrade`",
		"### `vault-sync version`",
		"[`vault-sync pull`](#vault-sync-pull)",
		"| `-c, --config string` |",
		"| `-n, --dry-run` |",
		"--version` | Version for vault-sync. |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, unwanted := range []string{updateCheckCommand, "completion", "Help for pull"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("unexpected %q in output", unwanted)
		}
	}
}
