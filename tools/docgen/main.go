// Command docgen regenerates the command reference in README.md from the
// vault-sync command tree. Run it with `task docs`; `-check` fails instead of
// writing when the file is out of date (used by CI).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/jordigarciaventura/vault-sync/internal/cli"
)

const (
	beginMarker = "<!-- BEGIN GENERATED COMMANDS (task docs) -->\n"
	endMarker   = "<!-- END GENERATED COMMANDS -->"
)

func main() {
	file := flag.String("file", "README.md", "Markdown file containing the generated-commands markers")
	check := flag.Bool("check", false, "exit with status 1 if the file is out of date instead of writing it")
	flag.Parse()

	if err := run(*file, *check); err != nil {
		fmt.Fprintln(os.Stderr, "docgen:", err)
		os.Exit(1)
	}
}

func run(file string, check bool) error {
	current, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	start := bytes.Index(current, []byte(beginMarker))
	end := bytes.Index(current, []byte(endMarker))
	if start < 0 || end < start {
		return fmt.Errorf("%s: markers %q and %q not found", file, beginMarker[:len(beginMarker)-1], endMarker)
	}

	var docs bytes.Buffer
	if err := cli.WriteMarkdownDocs(&docs); err != nil {
		return err
	}

	var updated bytes.Buffer
	updated.Write(current[:start+len(beginMarker)])
	updated.WriteString("\n")
	updated.Write(docs.Bytes())
	updated.WriteString("\n")
	updated.Write(current[end:])

	if bytes.Equal(current, updated.Bytes()) {
		return nil
	}
	if check {
		return fmt.Errorf("%s is out of date; run `task docs` and commit the result", file)
	}
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	return os.WriteFile(file, updated.Bytes(), info.Mode().Perm())
}
