package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jordigarciaventura/vault-sync/internal/updater"
)

func TestUpdateNoticeEveryInvocation(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv(updater.DisableEnv, "")
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"version"}, {"--version"}, {"pull", "--help"}, {"unknown"}, {"version", "--unknown"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			hits := 0
			fail := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits++
				if fail {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = fmt.Fprint(w, `{"tag_name":"v1.1.0"}`)
			}))
			defer srv.Close()
			now := time.Now()
			checker := &updater.Checker{Current: "v1.0.0", TTL: 10 * time.Minute, CacheFile: filepath.Join(t.TempDir(), "update.json"), APIBase: srv.URL, Now: func() time.Time { return now }}
			for i := 0; i < 4; i++ {
				if i == 2 {
					now = now.Add(10 * time.Minute)
				}
				if i == 3 {
					now = now.Add(10 * time.Minute)
					fail = true
				}
				var stdout, stderr bytes.Buffer
				a := &app{registry: Registry(), stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}
				code := execute(context.Background(), args, a, checker)
				wantCode := 0
				if len(args) > 0 && (args[0] == "unknown" || args[len(args)-1] == "--unknown") {
					wantCode = 1
				}
				if code != wantCode {
					t.Fatalf("exit=%d, want %d: %s", code, wantCode, stderr.String())
				}
				if !strings.Contains(stderr.String(), "Latest:    v1.1.0") {
					t.Fatalf("invocation %d missing notice: %s", i, stderr.String())
				}
				if strings.Contains(stdout.String(), "Latest:") {
					t.Fatal("notice leaked into stdout")
				}
				wantHits := []int{1, 1, 2, 3}[i]
				if hits != wantHits {
					t.Fatalf("requests=%d, want %d", hits, wantHits)
				}
			}
		})
	}
}

func TestUpdateCheckDisabled(t *testing.T) {
	for _, reason := range []string{"opt-out", "CI", "dev"} {
		t.Run(reason, func(t *testing.T) {
			t.Setenv("CI", "")
			t.Setenv(updater.DisableEnv, "")
			current := "v1.0.0"
			switch reason {
			case "opt-out":
				t.Setenv(updater.DisableEnv, "1")
			case "CI":
				t.Setenv("CI", "true")
			case "dev":
				current = "dev"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("unexpected update request")
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer srv.Close()
			var out bytes.Buffer
			a := &app{registry: Registry(), stdout: &out, stderr: &out}
			if code := execute(context.Background(), []string{"version"}, a, &updater.Checker{Current: current, APIBase: srv.URL}); code != 0 {
				t.Fatalf("exit=%d", code)
			}
		})
	}
}

func TestUpdateNoticeStyles(t *testing.T) {
	for _, colored := range []outputStyle{false, true} {
		got := styleUpdateNotice("1.2.0", "1.3.0", colored)
		for _, want := range []string{
			colored.paint(colorBold+";"+colorYellow, "A new version of vault-sync is available!"),
			colored.paint(colorCyan, "vault-sync self-upgrade"),
			"Installed: v1.2.0", "Latest:    v1.3.0",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("notice missing %q: %q", want, got)
			}
		}
		if !bool(colored) && strings.Contains(got, "\x1b") {
			t.Fatalf("plain notice contains escapes: %q", got)
		}
		if strings.Contains(got, "brew upgrade") {
			t.Fatalf("notice recommends Homebrew: %q", got)
		}
	}
}
