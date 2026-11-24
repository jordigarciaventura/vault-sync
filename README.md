# vault-sync

[![CI](https://github.com/jordigarciaventura/vault-sync/actions/workflows/ci.yml/badge.svg)](https://github.com/jordigarciaventura/vault-sync/actions/workflows/ci.yml)

Sync your project's secret files (`.env`, keys, certificates) with Bitwarden or
Infisical.

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

This creates a `secrets.yaml` like this:

```yaml
backend:
  type: bitwarden
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

Both commands prompt you to log in when needed. Commit `secrets.yaml`, which
contains only configuration; keep secret files out of
version control.

## Documentation

- [Configuration](docs/configuration.md) — manifest settings and automation.
- [Providers](docs/providers.md) — Bitwarden and Infisical setup.
- [Command reference](docs/commands.md) — commands and flags, also available with `vault-sync --help`.
- [Sync behavior](docs/usage.md) — output, backups and dry runs.
- [Development](DEVELOPMENT.md) — building, testing, architecture and releases.

## License

[MIT](LICENSE)
