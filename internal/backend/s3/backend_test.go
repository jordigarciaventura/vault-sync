package s3

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func TestRoundTrip(t *testing.T) {
	for _, content := range [][]byte{nil, []byte("hello\n"), {0, 255, 128, 10}} {
		t.Run(stringLength(content), func(t *testing.T) {
			objects := map[string][]byte{}
			authCalls := 0
			b := &Backend{account: "123456789012", run: func(_ context.Context, input []byte, args ...string) ([]byte, error) {
				if args[0] == "sts" {
					authCalls++
					return []byte(`{"Account":"123456789012"}`), nil
				}
				if args[0] != "s3" || args[1] != "cp" {
					t.Fatalf("unexpected command: %v", args)
				}
				if args[2] == "-" {
					objects[args[3]] = bytes.Clone(input)
					return nil, nil
				}
				data, ok := objects[args[2]]
				if !ok {
					return nil, backend.ErrNotFound
				}
				return data, nil
			}}
			ctx := context.Background()
			if err := b.StoreFile(ctx, "bucket/project", "dir/file", content); err != nil {
				t.Fatal(err)
			}
			if _, ok := objects["s3://bucket/project/dir/file"]; !ok {
				t.Fatal("incorrect key")
			}
			r, err := b.ResolveFile(ctx, "bucket/project", "dir/file")
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(r)
			r.Close()
			if err != nil || !bytes.Equal(got, content) {
				t.Fatalf("round trip failed: %v", err)
			}
			_, err = b.ResolveFile(ctx, "bucket/project", "missing")
			if !errors.Is(err, backend.ErrNotFound) {
				t.Fatalf("missing: %v", err)
			}
			if authCalls != 1 {
				t.Fatalf("authentication calls: %d", authCalls)
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
	for _, tc := range []struct {
		name, response string
		want           []string
		fail           bool
	}{
		{"nested", `{"Contents":[{"Key":"project/dir/z"},{"Key":"project/dir/a"},{"Key":"project/dir/"},{"Key":"project/dir-other/x"},{"Key":"other/dir/x"}]}`, []string{"dir/a", "dir/z"}, false},
		{"empty", `{}`, []string{}, false},
		{"unsafe", `{"Contents":[{"Key":"project/dir/../escape"}]}`, nil, true},
		{"invalid", `not-json`, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Backend{authenticated: true, run: func(_ context.Context, _ []byte, args ...string) ([]byte, error) {
				want := []string{"s3api", "list-objects-v2", "--bucket", "bucket", "--prefix", "project/dir/"}
				if !reflect.DeepEqual(args, want) {
					t.Fatalf("args: %v", args)
				}
				return []byte(tc.response), nil
			}}
			got, err := b.ListFiles(context.Background(), "bucket/project", "dir")
			if (err != nil) != tc.fail || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, error %v", got, err)
			}
		})
	}
}

func TestAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, response, account string
		runErr                  error
		fail                    bool
	}{
		{"valid", `{"Account":"123"}`, "123", nil, false},
		{"mismatch", `{"Account":"456"}`, "123", nil, true},
		{"malformed", `secret-output`, "", nil, true},
		{"missing", `{}`, "", nil, true},
		{"failed", "", "", errors.New("request failed"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Backend{account: tc.account, run: func(context.Context, []byte, ...string) ([]byte, error) { return []byte(tc.response), tc.runErr }}
			err := b.Authenticate(context.Background())
			if errors.Is(err, backend.ErrAuthentication) != tc.fail {
				t.Fatalf("error: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret-output") {
				t.Fatal("leaked output")
			}
		})
	}
}

func TestValidationIsLazy(t *testing.T) {
	t.Setenv(BinaryEnv, "/does/not/exist")
	for _, cfg := range []backend.Config{{}, {Account: "123"}} {
		b, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		b.Close()
	}
	for _, cfg := range []backend.Config{{Environment: "dev"}, {Server: "https://example.com"}} {
		if _, err := New(cfg); err == nil {
			t.Fatal("accepted unsupported setting")
		}
	}
	b := &Backend{run: func(context.Context, []byte, ...string) ([]byte, error) {
		t.Fatal("invalid path reached AWS")
		return nil, nil
	}}
	if err := b.StoreFile(context.Background(), "bucket/prefix", "../escape", nil); err == nil {
		t.Fatal("accepted unsafe filename")
	}
}
