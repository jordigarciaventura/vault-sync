package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/config"
	"github.com/jordigarciaventura/vault-sync/internal/sync"
)

func TestResultStyles(t *testing.T) {
	for _, tt := range []struct {
		name                  string
		action                sync.Action
		dry                   bool
		err                   error
		marker, detail, color string
	}{
		{"created", sync.ActionCreated, false, nil, "✓", "(created)", colorGreen},
		{"updated", sync.ActionUpdated, false, nil, "✓", "(updated)", colorYellow},
		{"unchanged", sync.ActionUnchanged, false, nil, "-", "(unchanged)", colorMuted},
		{"pushed", sync.ActionPushed, false, nil, "✓", "", colorGreen},
		{"dry run", sync.ActionCreated, true, nil, "-", "(would create)", colorCyan},
		{"failed", "", false, errors.New("unavailable"), "✗", "unavailable", colorRed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := sync.Result{File: config.File{Path: "example.txt"}, Action: tt.action, DryRun: tt.dry, Err: tt.err}
			for _, colored := range []outputStyle{false, true} {
				var out bytes.Buffer
				printStyledResult(&out, r, colored)
				got := out.String()
				if !strings.HasPrefix(got, colored.paint(tt.color, tt.marker)+" example.txt") || !strings.Contains(got, tt.detail) {
					t.Fatalf("unexpected output: %q", got)
				}
				if !bool(colored) && strings.Contains(got, "\x1b") {
					t.Fatalf("plain output contains escapes: %q", got)
				}
			}
		})
	}
}

func TestStyleForPlainOutput(t *testing.T) {
	for _, env := range []struct{ noColor, term string }{{"", "xterm"}, {"1", "xterm"}, {"", "dumb"}} {
		t.Setenv("NO_COLOR", env.noColor)
		t.Setenv("TERM", env.term)
		if styleFor(&bytes.Buffer{}) {
			t.Fatal("redirected output must remain plain")
		}
	}
}
