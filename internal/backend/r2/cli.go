package r2

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/jordigarciaventura/vault-sync/internal/backend"
	"github.com/jordigarciaventura/vault-sync/internal/toolinstall"
)

// Tool resolves Wrangler lazily through PATH or Homebrew. It is distributed via npm,
// not verified standalone GitHub release binaries.
var Tool = toolinstall.Tool{Name: "Cloudflare Wrangler", Binary: "wrangler", PathEnv: "VAULT_SYNC_WRANGLER_PATH", BrewFormula: "cloudflare-wrangler", ManualInstall: "npm install --global wrangler (https://developers.cloudflare.com/workers/wrangler/install-and-update/)"}

func newRunner() func(context.Context, bool, ...string) ([]byte, error) {
	var binary string
	return func(ctx context.Context, interactive bool, args ...string) ([]byte, error) {
		if binary == "" {
			var err error
			binary, err = toolinstall.Find(ctx, Tool)
			if err != nil {
				return nil, err
			}
		}
		// Do not let Wrangler discover a project's config or load its .env files.
		dir, err := os.MkdirTemp("", "vault-sync-wrangler-")
		if err != nil {
			return nil, fmt.Errorf("prepare Wrangler working directory: %w", err)
		}
		defer os.RemoveAll(dir)
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "WRANGLER_SEND_METRICS=false", "CI=true")
		if interactive {
			cmd.Env = append(cmd.Env, "CI=false")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
			err = cmd.Run()
		} else {
			var out []byte
			out, err = cmd.Output()
			if err == nil {
				return out, nil
			}
		}
		if err == nil {
			return nil, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Both streams may contain credentials. Never return any provider output.
		return nil, fmt.Errorf("%w: Wrangler authentication failed; run `wrangler login` or upgrade Wrangler to a version supporting `auth token --json`; automation can set CLOUDFLARE_API_TOKEN", backend.ErrAuthentication)
	}
}
