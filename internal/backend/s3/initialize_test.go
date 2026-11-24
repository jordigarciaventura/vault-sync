package s3

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
)

func TestInitializeBucket(t *testing.T) {
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		t.Run(region, func(t *testing.T) {
			var calls [][]string
			b := &Backend{region: region, run: func(_ context.Context, _ []byte, args ...string) ([]byte, error) {
				calls = append(calls, slices.Clone(args))
				switch args[1] {
				case "get-caller-identity":
					return []byte(`{"Account":"123456789012"}`), nil
				case "head-bucket":
					return nil, backend.ErrNotFound
				case "create-bucket", "put-public-access-block":
					return []byte(`{}`), nil
				default:
					t.Fatalf("unexpected AWS operation: %v", args)
					return nil, nil
				}
			}}
			setup, err := b.InitializeProject(context.Background(), "my-secret-bucket/project")
			if err != nil {
				t.Fatal(err)
			}
			if setup.Project != "my-secret-bucket/project" || setup.Config.Region != region || setup.Config.Account != "123456789012" {
				t.Fatalf("setup: %+v", setup)
			}
			want := []string{"s3api", "create-bucket", "--bucket", "my-secret-bucket", "--object-ownership", "BucketOwnerEnforced"}
			if region != "us-east-1" {
				want = append(want, "--create-bucket-configuration", "LocationConstraint="+region)
			}
			if len(calls) != 4 || !reflect.DeepEqual(calls[2], want) || calls[3][1] != "put-public-access-block" {
				t.Fatalf("calls: %v", calls)
			}
		})
	}
}

func TestInitializeExistingOrInaccessibleBucket(t *testing.T) {
	for _, tc := range []struct {
		name     string
		headErr  error
		location string
		fail     bool
	}{
		{"owned", nil, `{"LocationConstraint":null}`, false},
		{"wrong-region", nil, `{"LocationConstraint":"eu-west-1"}`, true},
		{"denied", errors.New("access denied"), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Backend{region: "us-east-1", account: "123", authenticated: true, run: func(_ context.Context, _ []byte, args ...string) ([]byte, error) {
				switch args[1] {
				case "head-bucket":
					if !slices.Contains(args, "--expected-bucket-owner") {
						t.Fatal("missing owner check")
					}
					return nil, tc.headErr
				case "get-bucket-location":
					return []byte(tc.location), nil
				default:
					t.Fatalf("existing bucket must not be mutated: %v", args)
					return nil, nil
				}
			}}
			_, err := b.InitializeProject(context.Background(), "my-bucket")
			if (err != nil) != tc.fail {
				t.Fatalf("error: %v", err)
			}
		})
	}
}
func TestInitializeInvalidBucketBeforeAuthentication(t *testing.T) {
	for _, name := range []string{"UPPERCASE", "192.168.0.1", "bad..bucket", "ab", "bucket--x-s3"} {
		b := &Backend{region: "us-east-1", run: func(context.Context, []byte, ...string) ([]byte, error) {
			t.Fatal("invalid bucket reached AWS")
			return nil, nil
		}}
		if _, err := b.InitializeProject(context.Background(), name); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
}
func TestBrowserAuthentication(t *testing.T) {
	for _, interactive := range []bool{true, false} {
		calls, logins := 0, 0
		b := &Backend{interactive: func() bool { return interactive }, login: func(context.Context) error { logins++; return nil }, run: func(context.Context, []byte, ...string) ([]byte, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("credentials expired")
			}
			return []byte(`{"Account":"123"}`), nil
		}}
		err := b.Authenticate(context.Background())
		if interactive {
			if err != nil || logins != 1 || calls != 2 {
				t.Fatalf("login failed: %v", err)
			}
		} else if !errors.Is(err, backend.ErrAuthentication) || logins != 0 {
			t.Fatal("non-interactive authentication prompted")
		}
	}
}
func TestCreationFailure(t *testing.T) {
	b := &Backend{account: "123", region: "us-east-1", authenticated: true, run: func(_ context.Context, _ []byte, args ...string) ([]byte, error) {
		if args[1] == "head-bucket" {
			return nil, backend.ErrNotFound
		}
		if args[1] == "create-bucket" {
			return nil, errors.New("denied")
		}
		t.Fatal("continued after creation failure")
		return nil, nil
	}}
	if _, err := b.InitializeProject(context.Background(), "my-bucket"); err == nil || !strings.Contains(err.Error(), "create S3 bucket") {
		t.Fatalf("error: %v", err)
	}
}
