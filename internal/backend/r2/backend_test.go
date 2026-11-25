package r2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

const testAccount = "0123456789abcdef0123456789abcdef"

func testBackend(t *testing.T, handler http.HandlerFunc) *Backend {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	value, err := New(backend.Config{Account: testAccount, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	b := value.(*Backend)
	b.apiBase = server.URL
	b.http = server.Client()
	b.getenv = func(key string) string {
		if key == "CLOUDFLARE_API_TOKEN" {
			return "test-token"
		}
		return ""
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	for _, content := range [][]byte{nil, []byte("hello\n"), {0, 255, 128, 10}} {
		t.Run(stringLength(content), func(t *testing.T) {
			objects := map[string][]byte{}
			b := testBackend(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing authorization")
				}
				if !strings.Contains(r.URL.EscapedPath(), "/objects/project/dir/file%20%23%3F%25") && !strings.HasSuffix(r.URL.Path, "/missing") {
					t.Errorf("incorrect key: %s", r.URL.EscapedPath())
				}
				if r.Method == http.MethodPut {
					data, _ := io.ReadAll(r.Body)
					objects[r.URL.Path] = data
					if r.Header.Get("Content-Type") != "application/octet-stream" {
						t.Error("incorrect upload content type")
					}
					_, _ = io.WriteString(w, `{"success":true,"result":{}}`)
					return
				}
				data, ok := objects[r.URL.Path]
				if !ok {
					w.WriteHeader(404)
					_, _ = io.WriteString(w, "SECRET")
					return
				}
				_, _ = w.Write(data)
			})
			ctx := context.Background()
			if err := b.StoreFile(ctx, "bucket/project", "dir/file #?%", content); err != nil {
				t.Fatal(err)
			}
			r, err := b.ResolveFile(ctx, "bucket/project", "dir/file #?%")
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(r)
			r.Close()
			if err != nil || !bytes.Equal(got, content) {
				t.Fatalf("round trip: %v", err)
			}
			_, err = b.ResolveFile(ctx, "bucket/project", "missing")
			if !errors.Is(err, backend.ErrNotFound) || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("missing: %v", err)
			}
		})
	}
}
func stringLength(b []byte) string {
	if len(b) == 0 {
		return "empty"
	}
	if b[0] == 0 {
		return "binary"
	}
	return "text"
}

func TestListing(t *testing.T) {
	calls := 0
	b := testBackend(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("prefix") != "project/dir/" {
			t.Error("incorrect prefix")
		}
		if calls == 1 {
			_, _ = io.WriteString(w, `{"success":true,"result":[{"key":"project/dir/z"},{"key":"project/dir/"},{"key":"other/dir/x"}],"result_info":{"cursor":"next+page","is_truncated":true}}`)
			return
		}
		if r.URL.Query().Get("cursor") != "next+page" {
			t.Error("incorrect cursor")
		}
		_, _ = io.WriteString(w, `{"success":true,"result":[{"key":"project/dir/a"},{"key":"project/dir/z"},{"key":"project/dir-other/x"}]}`)
	})
	got, err := b.ListFiles(context.Background(), "bucket/project", "dir")
	if err != nil || !reflect.DeepEqual(got, []string{"dir/a", "dir/z"}) || calls != 2 {
		t.Fatalf("listing: %v, %v, calls %d", got, err, calls)
	}
}

func TestInvalidListing(t *testing.T) {
	for _, body := range []string{`not-json`, `{"success":false}`, `{"success":true,"result":[{"key":"prefix/dir/../escape"}]}`, `{"success":true,"result":[],"result_info":{"is_truncated":true}}`, `{"success":true,"result":[],"result_info":{"is_truncated":true,"cursor":"repeat"}}`} {
		b := testBackend(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
		if _, err := b.ListFiles(context.Background(), "bucket/prefix", "dir"); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestValidation(t *testing.T) {
	for _, cfg := range []backend.Config{{}, {Account: testAccount}, {Account: testAccount, Region: "auto"}} {
		b, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		b.Close()
	}
	for _, cfg := range []backend.Config{{Account: "bad"}, {Server: "https://example.com"}, {Environment: "dev"}, {Region: "eu-west-1"}} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
	b, _ := New(backend.Config{})
	if !errors.Is(b.Authenticate(context.Background()), backend.ErrAuthentication) {
		t.Fatal("accepted missing account")
	}
	value := testBackend(t, func(http.ResponseWriter, *http.Request) { t.Error("unsafe input reached API") })
	if err := value.StoreFile(context.Background(), "bucket", "../escape", nil); err == nil {
		t.Fatal("accepted unsafe key")
	}
}

func TestInitialize(t *testing.T) {
	for _, missing := range []bool{false, true} {
		calls := 0
		b := testBackend(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/buckets/bucket") {
					t.Error("unexpected bucket check")
				}
				if missing {
					w.WriteHeader(404)
					return
				}
			} else {
				var data struct {
					Name string `json:"name"`
				}
				_ = json.NewDecoder(r.Body).Decode(&data)
				if r.Method != "POST" || data.Name != "bucket" {
					t.Error("unexpected creation")
				}
			}
			_, _ = io.WriteString(w, `{"success":true,"result":{}}`)
		})
		setup, err := b.InitializeProject(context.Background(), "bucket/prefix")
		if err != nil || setup.Project != "bucket/prefix" || setup.Config.Type != Type || setup.Config.Account != testAccount {
			t.Fatalf("setup: %+v, %v", setup, err)
		}
		want := 1
		if missing {
			want = 2
		}
		if calls != want {
			t.Fatalf("calls %d", calls)
		}
	}
}

func TestInitializeFailure(t *testing.T) {
	calls := 0
	b := testBackend(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(403)
		_, _ = io.WriteString(w, "SECRET")
	})
	if _, err := b.InitializeProject(context.Background(), "bucket"); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("error: %v", err)
	}
	for _, project := range []string{"ab", "Uppercase", "has.dot", "bucket/../escape"} {
		if _, err := b.InitializeProject(context.Background(), project); err == nil {
			t.Fatalf("accepted %s", project)
		}
	}
	if calls != 1 {
		t.Fatalf("unexpected requests: %d", calls)
	}
}

func TestEncodedBytesPreserved(t *testing.T) {
	data := []byte{0x1f, 0x8b, 0, 255}
	b := testBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("automatic decompression enabled")
		}
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(data)
	})
	r, err := b.ResolveFile(context.Background(), "bucket", "file")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("object bytes changed: %v", err)
	}
}
