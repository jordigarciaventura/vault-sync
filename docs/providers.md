# Providers

[Back to README](../README.md) · [Configuration](configuration.md)

## Provider CLIs

Each backend drives its provider's official CLI: `bw` for
[Bitwarden](#bitwarden-backend), `infisical` for [Infisical](#infisical-backend),
`aws` for [Amazon S3](#amazon-s3-backend), and `wrangler` for
[Cloudflare R2](#cloudflare-r2-backend).
Installing vault-sync does **not** install them. Instead, the first time a
command needs one (because `secrets.yaml` uses that backend, or
`vault-sync login --backend …`), vault-sync looks for it and installs it if
it is missing:

1. the path in `VAULT_SYNC_BW_PATH` / `VAULT_SYNC_INFISICAL_PATH` / `VAULT_SYNC_AWS_PATH` / `VAULT_SYNC_WRANGLER_PATH`, if set;
2. the executable on `PATH`;
3. a copy vault-sync downloaded earlier, in `~/.local/share/vault-sync/bin`
   (or `$XDG_DATA_HOME/vault-sync/bin`);
4. otherwise it installs it: with **Homebrew** (`brew install bitwarden-cli` or
   `brew install infisical` / `brew install awscli` / `brew install cloudflare-wrangler`) when `brew` is available, or else by downloading
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

## Bitwarden backend

The backend drives the official [Bitwarden Password Manager CLI](https://bitwarden.com/help/cli/)
(`bw`), so authentication, encryption and server configuration (including
self-hosted servers) are handled by Bitwarden itself. Init asks for
`backend.server` and an optional account email, logs in/unlocks and creates an
empty personal folder. The server URL is saved in the manifest. On a new
machine, vault-sync selects that server before logging in. If an existing
session is for another server, run `vault-sync logout` and retry; vault-sync
reports the mismatch before reading or writing files. Older manifests without
`backend.server` continue to use the CLI's configured server.
`bw` is installed on first use if it is missing (see
[Provider CLIs](#provider-clis)).

**Storage layout.** The first segment of `project` selects a vault **folder**.
Remaining segments form a virtual folder prefix on item and attachment names:
`project: vault-sync/example` stores `.env` as `example/.env` in `vault-sync`.
Bitwarden personal folders do not nest; this adapter uses personal folders,
not organization collections. Each
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

- *unauthenticated* → uses API key login when `BW_CLIENTID` and
  `BW_CLIENTSECRET` are set; otherwise runs `bw login` interactively
  (pre-filling a full `backend.account` email) or fails when prompts are unavailable;
- *locked* → unlocks using `BW_PASSWORD` when set, otherwise prompts through
  `bw unlock` in a terminal; scripts can also export `BW_SESSION=$(bw unlock --raw)`;
- the session key is kept in memory only and never written to disk.

The vault is synced (`bw sync`) before reading so recent changes are visible.

**`backend.account`** is checked against the logged-in email; it may be the full
email address or just the part before `@`. A mismatch aborts before anything is
read.

**Environment variables.**

| Variable              | Purpose                                                     |
| --------------------- | ----------------------------------------------------------- |
| `BW_SESSION`          | Existing session key to use instead of prompting            |
| `BW_CLIENTID` / `BW_CLIENTSECRET` | Personal API key credentials for non-interactive login |
| `BW_PASSWORD` | Master password for non-interactive unlocking (never passed as an argument) |
| `VAULT_SYNC_BW_PATH`  | Path to the `bw` executable to use (skips lookup and install) |

## Infisical backend

Init asks for the server, environment and optional account, authenticates, and
creates an empty secrets-management project (or reuses an accessible project).
It preserves the supplied project name, slug or ID (including any folder suffix)
and saves the server URL and environment in `secrets.yaml`.
A project path suffix creates the corresponding empty native folder.


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

**Project.** The first segment of `project` is the Infisical project's id, slug or name, tried in
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
Infisical folder and the base name is the secret key. Optional segments after
the project name prefix that folder: `project: vault-sync/example` stores
`backend/.env` in `/example/backend` inside project `vault-sync`.
Without a project prefix, the mapping is:

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

## Amazon S3 backend

```yaml
backend:
  type: s3
  account: "123456789012" # init saves the verified AWS account ID
  region: eu-west-1
project: my-secret-bucket/my-project
files:
  - path: .env
  - path: config/
```

The first project segment selects the **bucket**; remaining segments
prefix object keys. This example stores `.env` at `my-project/.env`.
Push replaces the current object; pull restores exact bytes, including binary
and empty files. Directory discovery lists keys recursively, automatically
paginating and ignoring folder-marker objects.

**Initialization.** `vault-sync init` asks for the region, optional expected AWS
account ID and a globally unique bucket name (optionally followed by `/prefix`).
It authenticates, creates a general-purpose bucket with owner-enforced object
ownership and explicitly blocks public access, then saves the bucket, region
and verified AWS account ID. No objects or prefix markers are uploaded.
An existing bucket is reused only after ownership and region checks; its policy
is left unchanged. For unavailable or inaccessible names, interactive init explains the ownership
or permission issue and asks for another globally unique name. Non-interactive
init fails without writing the manifest. AWS error codes are preserved with
safe, actionable hints; provider output is never printed. A denied ownership
check can mean the name belongs to another account or that `s3:ListBucket`
permission is missing. Regions such as `eu-south-2` require
[account opt-in](https://docs.aws.amazon.com/accounts/latest/reference/manage-acct-regions.html). Pull and push use existing buckets and never create them.
Enable bucket versioning if you need remote revision history.

**Installation.** Use AWS CLI v2. vault-sync finds it through
`VAULT_SYNC_AWS_PATH` or `PATH`, and installs `awscli` through Homebrew when
available. AWS does not publish binary releases on GitHub: without Homebrew,
install it using the [official AWS CLI installer](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html).
The GitHub download fallback described above applies to Bitwarden and Infisical.

**Authentication.** Existing AWS credentials, environment credentials, profiles
and IAM roles are reused. When credentials are missing or expired in an
interactive terminal, vault-sync starts `aws login` for browser authentication
(requires AWS CLI v2.32 or newer). Configured SSO profiles use `aws sso login`
instead. You can also log in beforehand with AWS tooling. `AWS_PROFILE` selects a profile; `AWS_REGION` / `AWS_DEFAULT_REGION`
select the default region when `backend.region` is absent. A saved region takes precedence. Credentials never belong in the manifest. vault-sync checks
`sts get-caller-identity` and verifies `backend.account` when provided.
`vault-sync login` and `logout` are not supported for this backend; manage the
AWS session with AWS tooling. `--non-interactive` never starts browser login;
provide usable credentials before running init, pull or push.
`backend.environment` and `backend.server` are unsupported.

Init additionally needs `s3:CreateBucket` and `s3:PutBucketPublicAccessBlock`
for new buckets; checking an existing bucket requires `s3:ListBucket` and
`s3:GetBucketLocation`.

Grant `s3:GetObject` and `s3:PutObject` on the selected objects and
`s3:ListBucket` on the bucket for directory discovery. Restrict access to the
project prefix where appropriate. SSE-KMS objects also require the relevant
KMS permissions. Objects use the bucket's default server-side encryption;
keep the bucket private and configure its encryption and access policies.

The adapter uses the official [AWS S3 CLI commands](https://docs.aws.amazon.com/cli/latest/reference/s3/)
and [paginated ListObjectsV2 API](https://docs.aws.amazon.com/cli/latest/reference/s3api/list-objects-v2.html).

## Cloudflare R2 backend

```yaml
backend:
  type: r2
  account: "0123456789abcdef0123456789abcdef" # Cloudflare account ID
project: my-secret-bucket/app
files:
  - path: .env
```

`project` selects an R2 bucket and optional key prefix. Files are stored as exact
object bytes, including empty and binary files. Directory discovery lists every
page of objects beneath the prefix. Pull and push require an existing bucket.

**Browser login.** Run `vault-sync init` and select `r2`. Enter your Cloudflare
account ID and bucket name; vault-sync reuses your Wrangler session or opens
Wrangler's browser OAuth login when no usable session exists. You do not need
to generate S3 access keys or configure the AWS CLI. You can also log in ahead
of time with `wrangler login`, or use `vault-sync login --backend r2` before
creating a manifest. With a manifest, `vault-sync login` reuses the selected
provider. `vault-sync logout` ends the shared Wrangler session.

**Installation.** Wrangler is found through `VAULT_SYNC_WRANGLER_PATH` or PATH,
then installed via Homebrew (`cloudflare-wrangler`) if available. Without
Homebrew, install it with `npm install --global wrangler`. Use a current
Wrangler version supporting `wrangler auth token --json`; Wrangler owns session
storage and automatically refreshes expired OAuth tokens. Token output is kept
in memory and never printed. Wrangler runs outside the project so it cannot
load project configuration or `.env` files.

**Automation.** `--non-interactive` never starts browser login. It reuses an
existing Wrangler session or accepts `CLOUDFLARE_API_TOKEN`, which takes
precedence and requires no Wrangler installation. Use a Cloudflare API bearer
token authorized for R2 operations in the selected account, rather than S3
access keys. Grant read access for pulls/listing and write access for pushes
and bucket creation. Environment tokens must be unset to log out. Credentials
never belong in `secrets.yaml`.

**Initialization.** Init checks the bucket before creating it if missing.
New buckets are private by default; existing public access settings remain
unchanged. Initialization requires permission to inspect the bucket and create
it when missing. `backend.account` is required for data operations; it selects
the account in API requests. `backend.server` and `backend.environment` are
unsupported. Region is unnecessary (`auto` is accepted for compatibility).
Jurisdiction-specific buckets are not currently supported.

The adapter uses the Cloudflare REST API for raw file uploads/downloads and
paginated object listings, authenticated with Wrangler's OAuth token or the
environment API token. The REST API limits uploads to 300 MB per file.

See [Wrangler authentication](https://developers.cloudflare.com/workers/wrangler/commands/general/#auth-token),
[object uploads](https://developers.cloudflare.com/api/resources/r2/subresources/buckets/subresources/objects/methods/upload/),
[object listings](https://developers.cloudflare.com/api/resources/r2/subresources/buckets/subresources/objects/methods/list/) and
[bucket creation rules](https://developers.cloudflare.com/r2/buckets/create-buckets/).
