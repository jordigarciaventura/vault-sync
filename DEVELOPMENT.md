# Development

Contributor guide for `vault-sync`. For what the tool does and how to use it,
see [README.md](README.md). The rules every contributor (human or AI) must
follow are in [AGENTS.md](AGENTS.md).

- [Requirements](#requirements)
- [Tasks](#tasks)
- [Running the development version](#running-the-development-version)
- [Tests](#tests)
- [Documentation](#documentation)
- [Git hooks and CI](#git-hooks-and-ci)
- [Architecture](#architecture)
- [Releasing](#releasing)

## Requirements

- Go ≥ 1.26
- [Task](https://taskfile.dev/) v3
- [golangci-lint](https://golangci-lint.run/) v2
- [GoReleaser](https://goreleaser.com/) ≥ 2.19 (only for release checks)
- Nothing provider-specific: `bw` / `infisical` are installed on first use
  (see `internal/toolinstall`) when you run `pull`/`push` against a real
  provider

## Tasks

```bash
task hooks        # once: install git hooks (.githooks)
task build        # bin/vault-sync
task run -- ARGS  # build, then run bin/vault-sync ARGS in the current directory
task install      # go install into $GOBIN (run it as `vault-sync` anywhere)
task test         # unit tests with -race
task lint         # golangci-lint
task fmt          # gofmt + goimports
task tidy         # go mod tidy
task docs         # regenerate the command reference in README.md
task docs-check   # fail if the command reference is out of date
task snapshot     # build release artifacts locally into dist/
task version      # print the current release version (VERSION)
task bump         # release: bump patch in VERSION, commit + tag (-- minor | -- major)
task clean        # remove bin/, dist/
task              # list all tasks
```

Before sending a change, run `go vet ./... && task test && task lint`.

## Running the development version

Anything after `--` is passed to the binary. `task run` executes in the
directory you call it from, so `secrets.yaml` is found there (or in a parent),
exactly as with an installed binary:

```bash
task run -- version -v               # git describe output (v0.1.0-3-gabc123-dirty), or v<VERSION>-dev without tags
task run -- help
task run -- pull --dry-run --verbose
```

To try it end to end without touching a real project, use a scratch directory
and a dedicated Bitwarden folder:

```bash
mkdir -p /tmp/vs-try && cd /tmp/vs-try
cat > secrets.yaml <<'EOF'
backend:
  type: bitwarden
project: vault-sync-dev        # a folder used only for testing
files:
  - path: demo/.env
EOF
mkdir -p demo && printf 'HELLO=world\n' > demo/.env

task -t ~/path/to/vault-sync/Taskfile.yml run -- push --dry-run
task -t ~/path/to/vault-sync/Taskfile.yml run -- push       # creates the folder + note
rm demo/.env
task -t ~/path/to/vault-sync/Taskfile.yml run -- pull       # restores demo/.env
```

(`task -t` points at the repo's Taskfile from another directory; alternatively
run `task build` once and call `~/path/to/vault-sync/bin/vault-sync` directly.)

Notes:

- `push` and `pull` without `--dry-run` talk to your real vault; keep test data
  in a throwaway folder and delete it from Bitwarden afterwards.
- A missing `bw` / `infisical` is installed on first use, exactly as for
  users. Make sure `which -a bw` lists only one Bitwarden CLI; otherwise set
  `VAULT_SYNC_BW_PATH=/opt/homebrew/bin/bw`.
- If you also have the Homebrew release installed, `vault-sync` on `PATH` is
  that one, not your checkout; use `task run`/`bin/vault-sync`, or
  `task install` and check `which vault-sync`.
- Update checks are skipped for `dev` builds (no tag yet).

## Tests

`task test` runs the unit tests with the race detector. They never touch the
network or a real vault: the Bitwarden adapter is tested against an in-memory
fake, and the updater against `httptest` servers.

**Integration tests** run against a real, *dedicated test* vault and are
excluded from the normal suite (`integration` build tag):

```bash
export BW_SESSION=$(bw unlock --raw)
export VAULT_SYNC_IT_PROJECT=vault-sync-integration   # folder they may write to
task test-integration
```

**End-to-end tests** ([test/e2e/](test/e2e/), `e2e` build tag) build the
`vault-sync` binary and run it against real Bitwarden and Infisical accounts
(a free account is enough for both). For each backend they push a random
`.env`, delete it locally, then check `pull --dry-run`, `pull` (created, 0600),
a second `pull` (unchanged) and a `pull` over a locally edited file (updated).

Use a **dedicated test account**: every run overwrites `.env` in the
`vault-sync-e2e` project/folder, which `push` creates if it does not exist.
The manifests live in [test/e2e/testdata/](test/e2e/testdata/)
(`bitwarden.yaml`, `infisical.yaml`) and pin no account, so whoever is logged
in runs them. `task test-e2e` tests every backend, so log in to all of them
first; authentication must work without a terminal (the tests fail rather
than prompt):

```bash
bw login && export BW_SESSION=$(bw unlock --raw)   # Bitwarden
infisical login                                    # Infisical (or export INFISICAL_TOKEN)

task test-e2e                                      # all backends
VAULT_SYNC_E2E=bitwarden task test-e2e             # only the listed ones (comma-separated)
```

`VAULT_SYNC_E2E` only narrows the selection; an unknown backend name in it
fails the run instead of silently skipping everything.

Optional overrides: `VAULT_SYNC_E2E_BITWARDEN_PROJECT` /
`VAULT_SYNC_E2E_INFISICAL_PROJECT` (project to use),
`VAULT_SYNC_E2E_BITWARDEN_ACCOUNT` / `VAULT_SYNC_E2E_INFISICAL_ACCOUNT`
(account the session must belong to) and `VAULT_SYNC_E2E_INFISICAL_SERVER`
(self-hosted instance). For manual runs against the same accounts, use the
testdata manifests directly, e.g.
`task run -- pull -c test/e2e/testdata/bitwarden.yaml` (the synced `.env`
next to them is git-ignored).

## Documentation

- [README.md](README.md) is for end users: what the tool does, how to install
  it and how to use it.
- This file is for contributors.

The **Commands** section of the README is generated from the cobra command
tree by [`tools/docgen`](tools/docgen/main.go) (using
`cli.WriteMarkdownDocs` in [internal/cli/docs.go](internal/cli/docs.go)). It
sits between the `<!-- BEGIN GENERATED COMMANDS … -->` and
`<!-- END GENERATED COMMANDS -->` markers; do not edit that part by hand.
After changing a command, a flag or any help text:

```bash
task docs
```

CI runs the same generator with `-check` and fails if the README is stale.

## Git hooks and CI

**Git hooks** (installed with `task hooks`, i.e. `git config core.hooksPath .githooks`):

| Hook         | Runs                                                                                   |
| ------------ | -------------------------------------------------------------------------------------- |
| `pre-commit` | blocks staged `.env`/`.key`/`.pem` files; `gofmt`, `go vet`, golangci-lint on new code |
| `commit-msg` | enforces [Conventional Commits](https://www.conventionalcommits.org/) (rules in [AGENTS.md](AGENTS.md#commit-messages)) |
| `pre-push`   | `go test -race ./...`                                                                  |

**CI** (`.github/workflows/ci.yml`) runs on every push to `main` and every pull
request: tidy check, README command-reference check and golangci-lint,
`go vet` + race tests on Linux and macOS, and a GoReleaser `check` + snapshot
build. Dependabot keeps Go modules and Actions up to date.

## Architecture

```
cmd/vault-sync/              main: signal handling + cli.Execute
internal/cli/                cobra commands; output formatting; update notice; docs generator
internal/config/             secrets.yaml parsing + validation (provider-independent)
internal/backend/            Backend interface, sentinel errors, Registry
internal/backend/bitwarden/  bw CLI adapter
internal/backend/infisical/  Infisical adapter: CLI for auth, REST API for secrets
internal/toolinstall/        finds provider CLIs; installs them on first use (brew or verified GitHub download)
internal/sync/               pull/push orchestration (depends only on backend.Backend)
internal/filesystem/         atomic, symlink-safe writes
internal/updater/            GitHub release check (cached, non-blocking) + self-upgrade
internal/version/            ldflags-injected build metadata
tools/docgen/                regenerates the README command reference
test/e2e/                    end-to-end tests of the binary against real accounts (e2e build tag)
scripts/bump-version.sh      bumps VERSION, commits and tags a release (task bump)
VERSION                      current release version (source of truth for release tags)
```

`internal/cli` → `internal/config` + `internal/sync` → `backend.Backend`
interface ← `internal/backend/{bitwarden,infisical}`. The sync service and config never
import a provider. To add a backend, implement `backend.Backend` in
`internal/backend/<name>/` and register its factory in `internal/cli/root.go`:

```go
r.Register("mybackend", mybackend.New)
```

If the backend drives an external CLI, describe it as a `toolinstall.Tool`
(binary, override variable, Homebrew formula, GitHub release asset) and
resolve the executable with `toolinstall.Find` lazily, on the first command,
so the tool is only installed when a project actually uses the backend. Do not
add provider CLIs as dependencies of the Homebrew cask.

**Why `bw` and not `bws`?** Bitwarden Secrets Manager (`bws`) stores key/value
secrets only: it has no attachments, no folders and requires a separate
product and machine-account tokens. The Password Manager CLI covers both
representations (notes and attachments) with the user's existing login. A
`bws`-based backend can be added later as a separate backend type without
changing manifests' `files` or `project`.

**Bitwarden binary notes.** The backend keeps text notes literal and stores
binary files as Base64 when the encoded body fits the 5,000-byte note limit
(3,750 original bytes). The `vault-sync.encoding=base64` custom field marks
encoded notes; unmarked notes are never decoded. Updates preserve unrelated
custom fields and remove the marker when switching to text or attachments.
Malformed encoded notes fail without returning partial content or including
note contents in errors. Unit tests cover free-account binary round trips,
size boundaries and transitions between storage representations.

**Why the REST API for Infisical secrets?** The `infisical secrets` commands
expand `${…}` references by default, append a newline in `--plain` mode,
and behave differently across CLI versions (`secrets set KEY=@file` stores the
literal `@file` when authenticated with a machine identity token). The adapter
therefore uses the CLI only for login state and tokens, and calls the API
with JSON bodies so values are exact and never appear in `ps`.

## Releasing

Releases are fully automated from Git tags.

### Versioning

The current version is tracked in [`VERSION`](VERSION) at the repository root
as plain `MAJOR.MINOR.PATCH` (no `v` prefix); `task version` prints it.
Release tags are `v` + that value (`0.1.0` → `v0.1.0`), and GoReleaser
injects the tag into the binary via `-ldflags`, so `vault-sync version`
reports it. Local builds (`task build`) use `git describe` instead, e.g.
`v0.1.0-3-gabc123-dirty`, falling back to `v<VERSION>-dev` when there is no
tag.

Do not edit `VERSION` or create tags by hand; use `task bump`:

| Command               | Example         | Use for                                     |
| --------------------- | --------------- | ------------------------------------------- |
| `task bump`           | 0.1.0 → 0.1.1   | fixes only (patch, the default; `fix` is an alias) |
| `task bump -- minor`  | 0.1.1 → 0.2.0   | new features, backwards compatible          |
| `task bump -- major`  | 0.2.0 → 1.0.0   | breaking changes (`type!:` commits)         |

`task bump` ([scripts/bump-version.sh](scripts/bump-version.sh)) refuses to
run unless you are on `main` with a clean working tree and the new tag does
not exist yet. It then writes `VERSION`, commits it as
`chore(release): vX.Y.Z` and creates the annotated tag `vX.Y.Z`. It does
**not** push. The Release workflow fails if the pushed tag does not match
`VERSION`.

Pick the part from the commits since the last tag
(`git log --oneline $(git describe --tags --abbrev=0)..`): any `!` means
major (minor while still on `0.x`), any `feat` means minor, otherwise patch.

### One-time setup

1. Create the public tap repository **`jordigarciaventura/homebrew-tap`** with a
   `main` branch (an initial README is enough; GoReleaser creates
   `Casks/vault-sync.rb`).
2. Create a **fine-grained personal access token**: *Repository access* → only
   `homebrew-tap`; *Permissions* → **Contents: Read and write**.
3. In `jordigarciaventura/vault-sync` → *Settings* → *Secrets and variables* →
   *Actions*, add it as **`HOMEBREW_TAP_GITHUB_TOKEN`**.
4. In *Settings* → *Actions* → *General*, keep "Read and write permissions"
   available to workflows (the release workflow requests `contents: write`).

### Publishing a version

```bash
git switch main && git pull
task test && task docs-check && task release-check   # optional local sanity check
task bump -- minor                                   # or: task bump / task bump -- major
git push origin main v1.2.0                          # the commit and the tag it printed
```

The **Release** workflow then:

1. checks that the tag matches `VERSION`, then runs `go vet` and the race
   test suite;
2. builds `darwin/{amd64,arm64}` and `linux/{amd64,arm64}` binaries with the
   version injected via `-ldflags`;
3. generates `checksums.txt` (SHA-256);
4. publishes the GitHub Release; its notes are the changelog GoReleaser
   builds from the commits since the previous tag (`changelog` in
   [.goreleaser.yaml](.goreleaser.yaml), `use: github`), grouped as
   *Breaking changes* (`type!:`), *Features* (`feat`), *Bug fixes* (`fix`),
   *Performance* (`perf`) and *Other changes*, followed by a "Full
   changelog" compare link. `docs`, `test`, `build`, `ci`, `style` and
   `chore` commits (including the `chore(release)` bump) are omitted, so
   commit subjects must follow the rules in
   [AGENTS.md → Commit messages](AGENTS.md#commit-messages);
5. commits the updated cask to `homebrew-tap`.

Versions follow [SemVer](https://semver.org/). Tags with a pre-release suffix
(`v1.3.0-rc.1`) are published as GitHub pre-releases, are **not** pushed to the
tap and do not trigger update notifications. `task bump` does not create
them: bump first, then tag the pre-release by hand with the version from
`VERSION` (`git tag -a v1.3.0-rc.1 -m v1.3.0-rc.1`); the release check accepts
`v<VERSION>` and `v<VERSION>-<suffix>`.

If a release fails halfway, delete the GitHub Release and the tag
(`git push --delete origin vX.Y.Z && git tag -d vX.Y.Z`), fix the problem on
`main` and re-create the tag on the fixed commit
(`git tag -a vX.Y.Z -m vX.Y.Z && git push origin main vX.Y.Z`); `VERSION`
already holds the right value, so do not run `task bump` again. If the tag
was never pushed, undo the bump locally with `git tag -d vX.Y.Z && git reset
--hard HEAD~1`.
