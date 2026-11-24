# vault-sync

[![CI](https://github.com/jordigarciaventura/vault-sync/actions/workflows/ci.yml/badge.svg)](https://github.com/jordigarciaventura/vault-sync/actions/workflows/ci.yml)

Sync your project's secret files (`.env`, keys, certificates) with Bitwarden,
Infisical or Amazon S3.

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

Init logs you in, creates an empty backend container and saves its location
in `secrets.yaml`. For S3, it asks for the bucket name and AWS region; for
Bitwarden and Infisical, it collects the server and account settings. For example:

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
credentials or starts browser login when needed. Commit `secrets.yaml`, which
contains only configuration; keep secret files out of version control.

## Documentation

- [Configuration](docs/configuration.md) — manifest settings and automation.
- [Providers](docs/providers.md) — Bitwarden, Infisical and Amazon S3 setup.
- [Command reference](docs/commands.md) — commands and flags, also available with `vault-sync --help`.
- [Sync behavior](docs/usage.md) — output, backups and dry runs.
- [Development](DEVELOPMENT.md) — building, testing, architecture and releases.

## License

[MIT](LICENSE)
