# Configuration

[Back to README](../README.md) · [Providers](providers.md)

`secrets.yaml` lives at the repository root and is safe to commit: it holds no
secret values or tokens. Provider container IDs and location settings are saved
so another machine can reach the same container after authenticating.

[`secrets.schema.json`](../secrets.schema.json) provides editor completion and
validation. `vault-sync init` includes the YAML language server directive and
a link to this repository in every generated manifest. For an existing manifest,
add these comments at the top:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/jordigarciaventura/vault-sync/main/secrets.schema.json
# vault-sync: https://github.com/jordigarciaventura/vault-sync
```

The schema covers the manifest structure and supported settings; the CLI also
checks normalized paths, duplicate destinations and Go regular expressions.

| Key                     | Required | Description                                                                 |
| ----------------------- | -------- | --------------------------------------------------------------------------- |
| `backend.type`          | yes      | Backend identifier. Supported: `bitwarden`, `infisical`.                    |
| `backend.account`       | no       | Expected provider account (see [providers](providers.md)).       |
| `backend.environment`   | no       | Provider environment (Infisical only; default `dev`).                       |
| `backend.server`        | no       | Provider instance URL (Bitwarden or Infisical).          |
| `project`               | yes      | Provider container, optionally followed by `/folder/subfolder` (see below). |
| `files[].path`          | yes      | Local file or directory **and** logical resource name.                            |
| `files[].regex`         | no       | Go regular expression filtering paths relative to a directory. |
| `files[].permissions`   | no       | Octal Unix mode, e.g. `"0600"` (default) or `"0640"`. Quote it.             |

**Paths.** Relative paths are resolved against the directory containing
`secrets.yaml` (not the current directory) and may not escape it with `..`.
Absolute paths are written as-is. The logical name used to look the file up in
the provider is the cleaned path without a leading slash:

| `path`                              | Logical name                       |
| ----------------------------------- | ---------------------------------- |
| `backend/.env`                      | `backend/.env`                     |
| `./backend/.env.production`         | `backend/.env.production`          |
| `/shared/keys/firmware-signing.key` | `shared/keys/firmware-signing.key` |

Validation rejects unknown keys, unknown backends, empty file lists,
duplicates, `~` paths, invalid regexes and invalid or setuid/setgid modes,
listing every problem at once.

**Directories and filters.** A directory entry synchronizes regular files
recursively, preserving their relative paths and applying the entry's permissions
to every file. Existing local directories are recognized automatically. On pull,
a missing local path is first checked for remote files beneath it; if none
exist, it is resolved as a single file. If both a remote directory and a file
share that path, the directory takes precedence. Use a trailing slash (such as
`config/`) to require a directory, including when it does not exist locally yet. `.` selects the manifest directory. Adding `regex` also
declares a directory; omit it to include all files.

```yaml
files:
  - path: config/
    regex: '(^|/)\.env(\..*)?$'
  - path: shared/keys/
    permissions: "0600"
```

The regex uses Go regexp syntax and matches slash-separated file paths relative
to the selected directory, including nested directories and hidden files.
Matching is unanchored unless you use `^` and `$`. Invalid expressions fail
validation. Pull discovers matching logical names in the provider, so it works
on a fresh clone; push discovers local files. Symlinks and other non-regular
files are skipped during directory uploads. An empty selection is an error.
Overlapping entries fail for repeated files rather than silently applying
different permissions. Dry runs perform discovery without writing or uploading.

When `--config` is not given, `vault-sync` looks for `secrets.yaml` in the
current directory and then in its parents, so it also works from
subdirectories.

### Automation and agents

Create and validate a manifest without prompts:

```bash
vault-sync init --non-interactive --backend bitwarden --project Example \
  --file backend/.env --file config/ --config secrets.yaml
vault-sync validate --config secrets.yaml
vault-sync pull --dry-run --non-interactive
vault-sync push --dry-run --non-interactive
```

`init --non-interactive` requires `--backend`, `--project` and at least one
`--file` (repeat for multiple paths). Existing manifests require `--force`.
Init authenticates and creates an empty container before saving the manifest;
it never uploads the listed files. It reuses an existing accessible container
without deleting or overwriting its secrets. Provider-specific settings can be
supplied with `--account`, `--server` and `--environment`:

```bash
vault-sync init --non-interactive --backend bitwarden --project my-project/app \
  --server https://vault.bitwarden.com --file .env
```

This requires working environment credentials or a provider session; it never
opens a browser or reads authentication prompts. Interactive init collects these
settings and drives provider login when needed. It saves the Bitwarden server
and folder, or the Infisical server, environment and resolved project ID.
A new machine needs its own credentials with access to the saved container.

If remote creation succeeds but a later step fails, the container is retained;
fix the reported problem and retry init. The manifest is written only after
successful initialization. A cancelled or failed remote setup leaves an existing
manifest unchanged.
`validate` checks parsing, paths and backend settings locally without installing
provider CLIs, logging in or checking remote resources. It exits with status
`1` for invalid configuration. It finds the manifest in parent directories,
just like pull and push.

`login`, `pull` and `push` accept `--non-interactive` to forbid authentication
prompts even in a terminal. Existing sessions and environment credentials work;
missing credentials fail with an actionable error. Dry runs still authenticate
and perform discovery, so they may install a missing provider CLI and update
its authentication or local cache, but do not write destination files, create
backups, or upload secrets. Pull previews report existing files as potential
overwrites without comparing their contents.

For Bitwarden, supply `BW_CLIENTID` and `BW_CLIENTSECRET` for API key login,
and `BW_PASSWORD` for unlocking on each pull/push invocation; an existing
`BW_SESSION` also works. `login` establishes the account login but keeps any
session key in memory only, so it cannot export a session to later commands.
These use the provider's [documented API key and password environment options](https://bitwarden.com/help/cli/).
For Infisical, supply `INFISICAL_TOKEN` and optionally `INFISICAL_DOMAIN`;
`login --non-interactive` checks the existing token or user session.
Credentials stay in environment variables rather than command arguments or
manifests, and are never printed.

## Update checks

| Variable                        | Default | Purpose                              |
| ------------------------------- | ------- | ------------------------------------ |
| `VAULT_SYNC_UPDATE_CHECK_TTL`   | `10m`   | Cache lifetime (Go duration)         |
| `VAULT_SYNC_NO_UPDATE_CHECK`    | unset   | Set to `1` to disable checks         |

Checks are skipped when `CI` is set and for development builds.
Notices go to stderr; use `vault-sync self-upgrade` to install an update.

### Project paths

Use `project: vault-sync/example` to share one provider container across
repositories. The first segment selects the Bitwarden folder or Infisical
project; the remaining segments prefix all remote filenames. For example,
`config/.env` lives at `example/config/.env` inside `vault-sync`. Pull and
directory discovery return names relative to `example`.

Paths must be relative, separated by `/`, with no empty, `.` or `..` segments,
backslashes, control characters or whitespace around segments. Plain project
names keep their existing layout. Infisical folder segments additionally allow
only letters, digits, `-` and `_`.

Changing `project` selects a different remote location; it does not move existing
secrets. Existing Bitwarden folders named literally `vault-sync/example` must
be moved manually to the `vault-sync` folder with `example/` prefixed to item
names before using this layout. A bulk migration command is not yet available.
