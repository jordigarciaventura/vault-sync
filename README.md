# vault-sync

[![CI](https://github.com/jordigarciaventura/vault-sync/actions/workflows/ci.yml/badge.svg)](https://github.com/jordigarciaventura/vault-sync/actions/workflows/ci.yml)

Restore a project's secret files (`.env`, signing keys, certificates, …) from
your secrets provider with one command after cloning:

```bash
git clone git@github.com:user/Example.git
cd Example
vault-sync pull
```

The project declares *which* files it needs in a provider-independent
`secrets.yaml`; the backend works out *how* they are stored. Supported
backends: [Bitwarden](#bitwarden-backend) and [Infisical](#infisical-backend).

- [Installation](#installation)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [Commands](#commands)
- [Bitwarden backend](#bitwarden-backend)
- [Infisical backend](#infisical-backend)
- [Update notifications](#update-notifications)
- [Security](#security)
- [Development](#development)

## Installation

```bash
brew tap jordigarciaventura/tap
brew install vault-sync
```

Use `brew upgrade vault-sync` (or `vault-sync self-upgrade`) to upgrade it.

Prebuilt binaries for macOS and Linux (amd64/arm64) are attached to every
[GitHub Release](https://github.com/jordigarciaventura/vault-sync/releases).
With a Go toolchain you can also run
`go install github.com/jordigarciaventura/vault-sync/cmd/vault-sync@latest`.

### Provider CLIs

Each backend drives its provider's official CLI: `bw` for
[Bitwarden](#bitwarden-backend), `infisical` for [Infisical](#infisical-backend).
Installing vault-sync does **not** install them. Instead, the first time a
command needs one (because `secrets.yaml` uses that backend, or
`vault-sync login --backend …`), vault-sync looks for it and installs it if
it is missing:

1. the path in `VAULT_SYNC_BW_PATH` / `VAULT_SYNC_INFISICAL_PATH`, if set;
2. the executable on `PATH`;
3. a copy vault-sync downloaded earlier, in `~/.local/share/vault-sync/bin`
   (or `$XDG_DATA_HOME/vault-sync/bin`);
4. otherwise it installs it: with **Homebrew** (`brew install bitwarden-cli` or
   `brew install infisical`) when `brew` is available, or else by downloading
   the latest official release from GitHub
   ([bitwarden/clients](https://github.com/bitwarden/clients/releases),
   [Infisical/cli](https://github.com/Infisical/cli/releases)) into the
   directory above, after verifying its SHA-256 digest.

Homebrew installs are upgraded with `brew upgrade` as usual. To update a
downloaded copy, delete it from `~/.local/share/vault-sync/bin`; the next
command downloads the latest release. To avoid any automatic installation,
install the CLI yourself or point the variable above at it.

Avoid having two Bitwarden CLIs at once (e.g. Homebrew's and a global
`npm install -g @bitwarden/cli`): whichever comes first on `PATH` wins and each
keeps its own login. Check with `which -a bw`, remove the extra one, or set
`VAULT_SYNC_BW_PATH` to the one you want.

## Quick start

1. Log in to Bitwarden once: `vault-sync login --backend bitwarden` (optional —
   `pull` and `push` log in automatically when needed; the Bitwarden CLI is
   installed on first use if it is missing).
2. Add a `secrets.yaml` at the root of your repository. `vault-sync init`
   creates one interactively (file name, backend, project, files to sync) and
   validates it before writing; or write it by hand:

   ```yaml
   backend:
     type: bitwarden
     account: you@example.com

   project: Example

   files:
     - path: backend/.env
     - path: backend/.env.production
     - path: /shared/keys/firmware-signing.key
       permissions: "0600"
   ```

3. Upload the files you already have: `vault-sync push`.
4. On any other machine, after cloning: `vault-sync pull`.

A successful pull looks like this:

```text
$ vault-sync pull
vault-sync

Project: Example
Backend: Bitwarden

✓ /shared/keys/firmware-signing.key (unchanged)
✓ backend/.env (updated)
✓ backend/.env.production (created)

Successfully synchronized 3 files.
```

`pull` writes every file atomically (temporary file in the same directory →
permissions → fsync → rename), creates missing directories with `0700`,
reports whether each file was created, updated or unchanged, and never deletes
files. If one file fails, the others are still processed and the command exits
with status `1`. `--dry-run` checks that every remote resource exists and
reports what would be written, without downloading attachments or touching the
filesystem.

## Configuration

`secrets.yaml` lives at the repository root and is safe to commit: it holds no
secret values, tokens or provider-specific ids.

| Key                     | Required | Description                                                                 |
| ----------------------- | -------- | --------------------------------------------------------------------------- |
| `backend.type`          | yes      | Backend identifier. Supported: `bitwarden`, `infisical`.                    |
| `backend.account`       | no       | Account the backend must be logged in as (see the backend's section).       |
| `backend.environment`   | no       | Provider environment (Infisical only; default `dev`).                       |
| `backend.server`        | no       | Provider instance URL, e.g. a self-hosted server (Infisical only).          |
| `project`               | yes      | Project / namespace in the provider (a Bitwarden folder, an Infisical project). |
| `files[].path`          | yes      | Local destination **and** logical resource name.                            |
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
duplicates, `~` paths, directory paths and invalid or setuid/setgid modes,
listing every problem at once.

When `--config` is not given, `vault-sync` looks for `secrets.yaml` in the
current directory and then in its parents, so it also works from
subdirectories.

## Commands

<!-- BEGIN GENERATED COMMANDS (task docs) -->

| Command | Description |
| ------- | ----------- |
| `vault-sync help` | Help about any command. |
| [`vault-sync init`](#vault-sync-init) | Create a secrets.yaml interactively. |
| [`vault-sync login`](#vault-sync-login) | Log in to the backend, or show who is logged in. |
| [`vault-sync logout`](#vault-sync-logout) | Log out of the backend. |
| [`vault-sync pull`](#vault-sync-pull) | Download all configured files from the backend. |
| [`vault-sync push`](#vault-sync-push) | Upload all configured files to the backend. |
| [`vault-sync self-upgrade`](#vault-sync-self-upgrade) | Upgrade vault-sync to the latest stable release. |
| [`vault-sync version`](#vault-sync-version) | Print the installed version. |

Every command accepts `-h, --help`; `vault-sync help <command>` shows the
same information in the terminal.

### `vault-sync`

vault-sync synchronizes the files listed in a project's secrets.yaml with a
remote secrets provider (Bitwarden or Infisical).

Typical workflow after cloning a repository:

```bash
vault-sync pull
```

```text
vault-sync [command] [flags]
```

| Flag | Description |
| ---- | ----------- |
| `-h, --help` | Help for vault-sync. |
| `-v, --version` | Version for vault-sync. |

### `vault-sync init`

Create a manifest by answering a few questions: the file name, the backend,
the project name in the provider and the files to synchronize.

The manifest is validated before it is written. An existing file is only
replaced after confirmation (or with --force). Optional backend settings
(account, environment, server) can be added to the file afterwards.

```text
vault-sync init [flags]
```

| Flag | Description |
| ---- | ----------- |
| `-f, --force` | Overwrite an existing manifest without asking. |

### `vault-sync login`

Log in to the backend interactively. If you are already logged in, show the
current account instead.

The backend, expected account and server are taken from secrets.yaml when
one is found; otherwise pass --backend. A session to another server than the
configured one is replaced by a login to that server. pull and push also log in
automatically when needed, so running this first is optional.

```text
vault-sync login [flags]
```

| Flag | Description |
| ---- | ----------- |
| `-b, --backend string` | Backend to use when no secrets.yaml is found (bitwarden, infisical). |
| `-c, --config string` | Path to the manifest (default: ./secrets.yaml, searched upwards; optional). |
| `-v, --verbose` | Print diagnostic details (never secret values). |

### `vault-sync logout`

Log out of the backend. The backend is taken from secrets.yaml when one is
found; otherwise pass --backend.

```text
vault-sync logout [flags]
```

| Flag | Description |
| ---- | ----------- |
| `-b, --backend string` | Backend to use when no secrets.yaml is found (bitwarden, infisical). |
| `-c, --config string` | Path to the manifest (default: ./secrets.yaml, searched upwards; optional). |
| `-v, --verbose` | Print diagnostic details (never secret values). |

### `vault-sync pull`

Download every file listed in secrets.yaml and write it to its destination.

Relative paths are resolved against the directory containing secrets.yaml.
Files are written atomically with the configured permissions (default 0600).

```text
vault-sync pull [flags]
```

| Flag | Description |
| ---- | ----------- |
| `-c, --config string` | Path to the manifest (default: ./secrets.yaml, searched upwards). |
| `-n, --dry-run` | Show planned operations without changing anything. |
| `-v, --verbose` | Print diagnostic details (never secret values). |

### `vault-sync push`

Upload every file listed in secrets.yaml to the backend, creating or
replacing the corresponding remote resources.

```text
vault-sync push [flags]
```

| Flag | Description |
| ---- | ----------- |
| `-c, --config string` | Path to the manifest (default: ./secrets.yaml, searched upwards). |
| `-n, --dry-run` | Show planned operations without changing anything. |
| `-v, --verbose` | Print diagnostic details (never secret values). |

### `vault-sync self-upgrade`

Upgrade vault-sync to the latest stable GitHub release.

Homebrew installations are upgraded with "brew upgrade"; other installations
are replaced in place after verifying the published SHA-256 checksum.

```text
vault-sync self-upgrade
```

### `vault-sync version`

Print the installed version.

```text
vault-sync version [flags]
```

| Flag | Description |
| ---- | ----------- |
| `-v, --verbose` | Also print commit and build date. |

<!-- END GENERATED COMMANDS -->

## Bitwarden backend

The backend drives the official [Bitwarden Password Manager CLI](https://bitwarden.com/help/cli/)
(`bw`), so authentication, encryption and server configuration (including
self-hosted servers via `bw config server`) are handled by Bitwarden itself.
`bw` is installed on first use if it is missing (see
[Provider CLIs](#provider-clis)).

**Storage layout.** `project` is a vault **folder** with that exact name. Each
file is stored as one of:

1. a **Secure Note** whose name is the logical name (e.g. `backend/.env`); the
   note body is the file content (plain text or Base64-encoded binary), or
2. an **attachment**, either on an item whose name is the logical name (the
   attachment is named after the file's base name, e.g. `firmware-signing.key`),
   or on any item in the folder with an attachment named exactly the logical
   name.

`vault-sync` finds the right representation automatically. If nothing matches
it fails with "not found"; if more than one resource matches (two notes with the
same name, a note *and* an attachment, two folders with the project name) it
fails with an ambiguity error instead of guessing.

`push` creates the folder if needed and stores UTF-8 text files (without NUL
bytes, ≤ 5,000 bytes) as plain Secure Notes. Small binary files, such as an
Android `upload.keystore`, are Base64-encoded into Secure Notes automatically:
up to **3,750 original bytes**, so the encoded body fits within 5,000 bytes.
Both work with a **free Bitwarden account**. Keep the original file path in
`secrets.yaml`; `pull` decodes binary notes back to the exact original bytes.
No manual encoding or decoding is needed.

Encoded notes carry a `vault-sync.encoding` custom field set to `base64`.
Leave that field intact; notes without it are always read literally, even if
they look like Base64. Older vault-sync versions do not decode these notes;
update vault-sync on every machine before pulling encoded files.

Files above these limits use attachments. Existing attachments remain
attachments; existing notes switch between plain text and Base64 as needed,
or convert to attachments when their stored body would exceed 5,000 bytes.
Attachments require a Bitwarden Premium or organization plan; without one,
pushing an oversized file fails with a clear error and removes any empty
item created for that failed upload.

**Authentication.** `vault-sync` reads `bw status`:

- *unauthenticated* → runs `bw login` interactively (pre-filling the email when
  `backend.account` is a full address); in scripts/CI it fails and asks you to
  log in first;
- *locked* → prompts for your master password through `bw unlock` when running
  in a terminal; in scripts/CI export `BW_SESSION=$(bw unlock --raw)` first;
- the session key is kept in memory only and never written to disk.

The vault is synced (`bw sync`) before reading so recent changes are visible.

**`backend.account`** is checked against the logged-in email; it may be the full
email address or just the part before `@`. A mismatch aborts before anything is
read.

**Environment variables.**

| Variable              | Purpose                                                     |
| --------------------- | ----------------------------------------------------------- |
| `BW_SESSION`          | Existing session key to use instead of prompting            |
| `VAULT_SYNC_BW_PATH`  | Path to the `bw` executable to use (skips lookup and install) |

## Infisical backend

Authentication is handled by the official
[Infisical CLI](https://infisical.com/docs/cli/overview) (`infisical`);
secrets are read and written through the Infisical REST API, so values never
appear in process arguments and are not run through reference expansion.
Infisical Cloud (US/EU) and self-hosted instances work. Without
`backend.server`, vault-sync uses the instance the CLI is logged in to
(`infisical login --domain …` or `INFISICAL_DOMAIN`); run `vault-sync login`
to see which one. `infisical` is installed on first use if it is missing (see
[Provider CLIs](#provider-clis)).

```yaml
backend:
  type: infisical
  environment: dev # optional, default: dev
  account: you@example.com # optional
  server: https://infisical.example.com # optional, default: the CLI's login
project: My Project # project name, slug or id
files:
  - path: .env
  - path: backend/.env.production
```

**Server.** `backend.server` pins the project to one Infisical instance (the
URL you open in the browser; a trailing `/api` is ignored). `pull` and `push`
then refuse a session on another instance, and logging in, whether through
`vault-sync login` or automatically, runs `infisical login --domain <server>`.
If you are logged in elsewhere, `vault-sync login` logs you in to the
configured server instead. With `INFISICAL_TOKEN`, the token is checked
against the configured server (unless `INFISICAL_DOMAIN` is set, which must
then match).

**Project.** `project` is the Infisical project's id, slug or name, tried in
that order. A name must match exactly, and if two accessible projects share
it, vault-sync asks you to use the slug instead. If no accessible project
matches, `push` creates it (`pull` reports it as not found) with the
default `dev`, `staging` and `prod` environments. The name is `project`, and so
is the slug when `project` is a valid one (lower-case letters, digits and `-`, at
least 5 characters); otherwise Infisical picks a slug. Creating projects needs
the permission to do so in your organization. To avoid near-duplicates,
nothing is created when a project's name or slug differs from `project` only
in case. Slugs are lower case and often
end in a random suffix (e.g. `my-project-a1b2`): look them up with
`infisical sm projects list` or in the project settings. A legacy service
token cannot list projects, so use the slug or id with it.

**Storage layout.** `backend.environment` is one of the project's environments. Each file is a **shared secret**
whose value is the file content. The directory part of the logical name is the
Infisical folder and the base name is the secret key:

| Logical name                       | Folder         | Secret key             |
| ---------------------------------- | -------------- | ---------------------- |
| `.env`                             | `/`            | `.env`                 |
| `backend/.env.production`          | `/backend`     | `.env.production`      |
| `shared/keys/firmware-signing.key` | `/shared/keys` | `firmware-signing.key` |

Infisical folder names may only contain letters, digits, `-` and `_`, so a path
such as `.github/token` is rejected with an error asking you to rename the
directory. Secret keys cannot contain `:`. Imports, references and personal
overrides are ignored: `pull` writes the stored value byte for byte.

`push` creates a missing project and folders, and creates or updates the secret. Values are
text: Infisical stores UTF-8 only and trims whitespace at both ends of a value
(keeping one final newline). A file that would not come back identical
(binary, leading whitespace, trailing blank lines or spaces, CRLF line
endings) is rejected before anything is sent. If the environment has a change
policy, the push fails and tells you to approve the request in Infisical.

**Authentication.** `vault-sync` reads `infisical login status`:

- `INFISICAL_TOKEN` set (machine identity access token, e.g. from
  `infisical login --method=universal-auth --plain`) → it is used, as the
  Infisical CLI itself does;
- otherwise the logged-in user is used; when nobody is logged in or the login
  has expired, `infisical login` runs interactively in a terminal, and scripts
  or CI fail with a hint;
- the access token is kept in memory only.

**`backend.account`** is checked against the logged-in user's email (the full
address or the part before `@`) or the machine identity's name or id.

**Environment variables.**

| Variable                    | Purpose                                                       |
| --------------------------- | ------------------------------------------------------------- |
| `INFISICAL_TOKEN`           | Machine identity access token to use instead of a user login   |
| `INFISICAL_DOMAIN`          | Infisical instance (read by the Infisical CLI)                 |
| `VAULT_SYNC_INFISICAL_PATH` | Path to the `infisical` executable to use (skips lookup and install) |

## Update notifications

Every command (including `help` and `version`) shows a notice on stderr when a
newer stable release exists:

```text
A new version of vault-sync is available!

Installed: v1.2.0
Latest:    v1.3.0

Update with:
brew upgrade vault-sync
```

The notice is rendered from a local cache (`~/Library/Caches/vault-sync/` on
macOS, `$XDG_CACHE_HOME/vault-sync/` on Linux). When the cache is older than
the TTL, a detached background process refreshes it from the GitHub Releases
API with a 3 s timeout, so commands never wait on the network and offline
machines are unaffected. Only the repository name is sent; nothing about your
project or secrets.

| Variable                        | Default | Purpose                              |
| ------------------------------- | ------- | ------------------------------------ |
| `VAULT_SYNC_UPDATE_CHECK_TTL`   | `12h`   | Cache lifetime (Go duration)         |
| `VAULT_SYNC_NO_UPDATE_CHECK`    | unset   | Set to `1` to disable checks         |

Checks are also skipped when `CI` is set, for development builds, and the
notice is only shown when stderr is a terminal. `vault-sync` never updates
itself unless you run `self-upgrade`, which uses `brew upgrade` for Homebrew
installs and otherwise downloads the release archive and verifies its SHA-256
checksum before replacing the binary.

## Security

- `secrets.yaml` contains no secret values; downloaded files default to `0600`.
- Secret contents never appear in output, logs or error messages; `--verbose`
  logs names, sizes and ids only.
- Secrets are passed to `bw` on stdin, never as command-line arguments.
- Writes are atomic and never follow a symlink at the destination; relative
  destinations whose directories resolve outside the project through a
  symlink are refused.
- No commands from the manifest are ever executed.

## Development

Building from source, running tests, the architecture and the release process
are described in [DEVELOPMENT.md](DEVELOPMENT.md).

## License

[MIT](LICENSE)
