package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// WriteMarkdownDocs renders a Markdown reference of every visible command,
// generated from the cobra command tree so it never drifts from the CLI.
// The output is deterministic (no version, dates or paths).
func WriteMarkdownDocs(w io.Writer) error {
	root := newRootCommand(&app{registry: Registry(), stdout: io.Discard, stderr: io.Discard})
	root.InitDefaultHelpCmd()

	cmds := []*cobra.Command{root}
	for _, c := range root.Commands() {
		if c.IsAvailableCommand() || c.Name() == "help" {
			cmds = append(cmds, c)
		}
	}

	var b bytes.Buffer
	b.WriteString("| Command | Description |\n| ------- | ----------- |\n")
	for _, c := range cmds[1:] {
		name := "`" + c.CommandPath() + "`"
		if !isHelp(c) {
			name = fmt.Sprintf("[%s](#%s)", name, anchor(c.CommandPath()))
		}
		fmt.Fprintf(&b, "| %s | %s |\n", name, sentence(c.Short))
	}
	b.WriteString("\nEvery command accepts `-h, --help`; `vault-sync help <command>` shows the\nsame information in the terminal.\n")

	for _, c := range cmds {
		if !isHelp(c) {
			writeCommand(&b, c)
		}
	}
	_, err := w.Write(b.Bytes())
	return err
}

func isHelp(c *cobra.Command) bool { return c.Name() == "help" && c.HasParent() }

func writeCommand(b *bytes.Buffer, c *cobra.Command) {
	isRoot := !c.HasParent()
	if isRoot {
		c.InitDefaultHelpFlag()
		c.InitDefaultVersionFlag()
	}
	fmt.Fprintf(b, "\n### `%s`\n\n", c.CommandPath())

	desc := c.Long
	if desc == "" {
		desc = sentence(c.Short)
	}
	b.WriteString(markdownText(desc))

	usage := c.UseLine()
	if c.HasAvailableSubCommands() {
		usage = c.CommandPath() + " [command] [flags]"
	}
	fmt.Fprintf(b, "\n```text\n%s\n```\n", usage)

	var rows []string
	c.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
		// -h/--help exists everywhere; document it once, on the root command.
		if f.Hidden || (f.Name == "help" && !isRoot) {
			return
		}
		rows = append(rows, fmt.Sprintf("| `%s` | %s |", flagName(f), flagUsage(f)))
	})
	if len(rows) > 0 {
		b.WriteString("\n| Flag | Description |\n| ---- | ----------- |\n")
		b.WriteString(strings.Join(rows, "\n") + "\n")
	}
}

func flagName(f *pflag.Flag) string {
	name := "--" + f.Name
	if f.Shorthand != "" {
		name = "-" + f.Shorthand + ", " + name
	}
	if typ, _ := pflag.UnquoteUsage(f); typ != "" {
		name += " " + typ
	}
	return name
}

func flagUsage(f *pflag.Flag) string {
	_, usage := pflag.UnquoteUsage(f)
	usage = sentence(usage)
	if f.DefValue != "" && f.DefValue != "false" {
		usage += fmt.Sprintf(" Default: `%s`.", f.DefValue)
	}
	return strings.ReplaceAll(usage, "|", `\|`)
}

// markdownText converts a cobra Long description to Markdown: paragraphs are
// kept as-is and indented paragraphs (command examples) become code blocks.
func markdownText(s string) string {
	var out []string
	for p := range strings.SplitSeq(strings.TrimSpace(s), "\n\n") {
		lines := strings.Split(p, "\n")
		indented := true
		for _, l := range lines {
			indented = indented && strings.HasPrefix(l, "  ")
		}
		if !indented {
			out = append(out, p)
			continue
		}
		for i, l := range lines {
			lines[i] = strings.TrimPrefix(l, "  ")
		}
		out = append(out, "```bash\n"+strings.Join(lines, "\n")+"\n```")
	}
	return strings.Join(out, "\n\n") + "\n"
}

// sentence capitalizes s and ends it with a period.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// anchor mirrors GitHub's heading anchors for "vault-sync pull" style titles.
func anchor(title string) string {
	return strings.ToLower(strings.ReplaceAll(title, " ", "-"))
}
