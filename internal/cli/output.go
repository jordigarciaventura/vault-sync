package cli

import (
	"io"
	"os"
	"strings"

	"github.com/jordigarciaventura/vault-sync/internal/updater"

	"golang.org/x/term"
)

type outputStyle bool

const (
	colorBold   = "1"
	colorRed    = "31"
	colorGreen  = "32"
	colorYellow = "33"
	colorCyan   = "36"
	colorMuted  = "90"
)

// styleFor checks the actual output stream, independently of stdin or stderr.
func styleFor(out io.Writer) outputStyle {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := out.(interface{ Fd() uintptr })
	return outputStyle(ok && term.IsTerminal(int(f.Fd())))
}

func (s outputStyle) paint(color, text string) string {
	if !s {
		return text
	}
	return "\x1b[" + color + "m" + text + "\x1b[0m"
}

func styleUpdateNotice(current, latest string, style outputStyle) string {
	notice := updater.Notice(current, latest)
	notice = strings.Replace(notice, "A new version of vault-sync is available!", style.paint(colorBold+";"+colorYellow, "A new version of vault-sync is available!"), 1)
	return strings.Replace(notice, "vault-sync self-upgrade", style.paint(colorCyan, "vault-sync self-upgrade"), 1)
}
