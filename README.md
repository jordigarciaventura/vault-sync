# vault-sync

[![CI](https://github.com/jordigarciaventura/vault-sync/actions/workflows/ci.yml/badge.svg)](https://github.com/jordigarciaventura/vault-sync/actions/workflows/ci.yml)

Sync your project's secret files (`.env`, keys, certificates) with Bitwarden,
Infisical, Amazon S3 or Cloudflare R2.

## Installation

```bash
brew tap jordigarciaventura/tap
brew install vault-sync
```

## Quick start

Run this in your project and answer the prompts:

```bash
vault-sync init
```

Init discovers `.env` and `.env.*` files below the manifest directory and lets
you select which to include. You can then enter other secret file or directory
paths; manual entries have no default.

Init logs you in, creates an empty backend container and saves its location
in `secrets.yaml`. For S3, it asks for the bucket name and AWS region; for
R2, it asks for the Cloudflare account ID; for Bitwarden and Infisical, it
collects the server and account settings. Successful interactive runs remember
your manifest path, backend, provider settings and project prefix for next time.
Forms keep built-in defaults visible alongside the last value, which is selected
by default. You can also enter another value.
For example, `vault-sync/example` makes the next project default
`vault-sync/<manifest folder name>`. The generated manifest looks like:

```yaml
backend:
  type: bitwarden
  server: https://vault.bitwarden.com
project: Example
files:
  - path: .env
```

Upload your existing files:

```bash
vault-sync push
```

On another machine, clone the repository and restore them:

```bash
vault-sync pull
```

Each machine authenticates with the selected provider; S3 reuses AWS
credentials or starts browser login when needed. R2 reuses Wrangler’s session
or opens browser login. Commit `secrets.yaml`, which contains only configuration; keep secret files out of version control.

## Documentation

- [Configuration](docs/configuration.md) — manifest settings and automation.
- [Providers](docs/providers.md) — Bitwarden, Infisical, Amazon S3 and Cloudflare R2 setup.
- [Command reference](docs/commands.md) — commands and flags, also available with `vault-sync --help`.
- [Sync behavior](docs/usage.md) — output, backups and dry runs.
- [Development](DEVELOPMENT.md) — building, testing, architecture and releases.

## License

[MIT](LICENSE)
