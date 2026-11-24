# Command reference

[Back to README](../README.md)

<!-- BEGIN GENERATED COMMANDS (task docs) -->

| Command | Description |
| ------- | ----------- |
| `vault-sync help` | Help about any command. |
| [`vault-sync init`](#vault-sync-init) | Create a secrets.yaml interactively or from flags. |
| [`vault-sync login`](#vault-sync-login) | Log in to the backend, or show who is logged in. |
| [`vault-sync logout`](#vault-sync-logout) | Log out of the backend. |
| [`vault-sync pull`](#vault-sync-pull) | Download all configured files from the backend. |
| [`vault-sync push`](#vault-sync-push) | Upload all configured files to the backend. |
| [`vault-sync self-upgrade`](#vault-sync-self-upgrade) | Upgrade vault-sync to the latest stable release. |
| [`vault-sync validate`](#vault-sync-validate) | Validate the configuration without contacting a backend. |
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

Use --non-interactive with --backend, --project and repeated --file flags
to create a manifest without prompts. --config selects the output path.

The manifest is validated before it is written. An existing file is only
replaced after confirmation (or with --force). Optional backend settings
(account, environment, server) can be added to the file afterwards.

```text
vault-sync init [flags]
```

| Flag | Description |
| ---- | ----------- |
| `-b, --backend string` | Backend to use (required with --non-interactive). |
| `-c, --config string` | Path of the manifest to create. Default: `secrets.yaml`. |
| `--file stringArray` | File or directory to synchronize; repeat for each entry (required with --non-interactive). Default: `[]`. |
| `-f, --force` | Overwrite an existing manifest without asking. |
| `--non-interactive` | Create a manifest from flags without prompting. |
| `--project string` | Project name (required with --non-interactive). |

### `vault-sync login`

Log in to the backend interactively. If you are already logged in, show the
current account instead.

Use --non-interactive to forbid prompts. Bitwarden accepts BW_CLIENTID and
BW_CLIENTSECRET for API key login; pull/push also accept BW_PASSWORD to unlock.
Infisical accepts INFISICAL_TOKEN. Existing provider sessions are reused.

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
| `--non-interactive` | Use existing sessions or environment credentials without prompting. |
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
| `--non-interactive` | Use existing sessions or environment credentials without prompting. |
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
| `--non-interactive` | Use existing sessions or environment credentials without prompting. |
| `-v, --verbose` | Print diagnostic details (never secret values). |

### `vault-sync self-upgrade`

Upgrade vault-sync to the latest stable GitHub release.

Homebrew installations are upgraded with "brew upgrade"; other installations
are replaced in place after verifying the published SHA-256 checksum.

```text
vault-sync self-upgrade
```

### `vault-sync validate`

Validate the manifest using the same parsing and validation rules as pull and push.
No provider CLI, authentication or remote access is required.

```text
vault-sync validate [flags]
```

| Flag | Description |
| ---- | ----------- |
| `-c, --config string` | Path to the manifest (default: ./secrets.yaml, searched upwards). |

### `vault-sync version`

Print the installed version.

```text
vault-sync version [flags]
```

| Flag | Description |
| ---- | ----------- |
| `-v, --verbose` | Also print commit and build date. |

<!-- END GENERATED COMMANDS -->
