# AGENTS.md

Guidance for AI coding agents (Claude Code, Codex, Cursor, …) working in this
repository. Read [README.md](README.md) (user docs) and
[DEVELOPMENT.md](DEVELOPMENT.md) (contributor docs) first.

## What this is

`vault-sync` is a small Go CLI that restores project files (`.env`, keys, …)
from a secrets provider using a declarative `secrets.yaml`. Backends:
Bitwarden (via the official `bw` CLI) and Infisical (`infisical` CLI for
authentication, REST API for secrets).

## Commands

| Task                    | Command                                         |
| ----------------------- | ----------------------------------------------- |
| Build                   | `task build` (→ `bin/vault-sync`)               |
| Unit tests (always run) | `task test` (`go test -race ./...`)             |
| E2E tests (real accounts)  | `task test-e2e` (see DEVELOPMENT.md)            |
| Lint                    | `task lint` (golangci-lint v2, `.golangci.yml`) |
| Format                  | `task fmt`                                      |
| Release config check    | `task release-check` (GoReleaser ≥ 2.19)        |
| Regenerate CLI docs     | `task docs` (README command reference)          |
| Current version         | `task version` (reads `VERSION`)                |
| Cut a release           | `task bump [-- minor\|major]` (user only)       |

Run `go vet ./... && task test` before declaring any change done. After
changing a command, flag or help text, run `task docs` (CI fails if
it is stale). Keep
`go.mod` tidy (CI fails otherwise).

## Keeping docs in sync (required)

A change is not done until [README.md](README.md) and
[DEVELOPMENT.md](DEVELOPMENT.md) still describe the code accurately. After
every change, check whether either file refers to what you touched, and
update it in the same change if it does. Do not leave this as a follow-up.

- **README.md** (user docs): installation, provider CLI install behaviour,
  quick start, `secrets.yaml` fields and validation, backend options and
  authentication (Bitwarden, Infisical), update notifications, security
  guarantees (permissions, atomic writes, what is logged), and examples.
  The command reference between the generated markers comes from
  `task docs`; do not edit it by hand.
- **DEVELOPMENT.md** (contributor docs): requirements and tool versions,
  `Taskfile.yml` tasks, running the dev build, tests and build tags, git
  hooks and CI, the architecture/layout description, and the release
  process.
- Also keep this file's **Layout**, **Commands** and rules sections current
  when packages, tasks or conventions change.
- Search both files (`grep -n`) for the names you changed (flags, fields,
  env vars, paths, tasks, versions) rather than relying on memory, and
  remove docs for anything you deleted.
- Mention in your summary which docs you updated, or that none needed it.

## Layout

```
cmd/vault-sync/          main: signal handling + cli.Execute
internal/cli/            cobra commands; output formatting; update notice
internal/config/         secrets.yaml parsing + validation (provider-independent)
internal/backend/        Backend interface, sentinel errors, Registry
internal/backend/bitwarden/  bw CLI adapter: client.go (exec), resolver.go (pure matching), backend.go
internal/backend/infisical/  cli.go (auth via infisical CLI), api.go (REST), layout.go (pure mapping), backend.go
internal/toolinstall/    finds provider CLIs; installs them on first use (brew, else SHA-256-verified GitHub download)
internal/sync/           pull/push orchestration (depends only on backend.Backend)
internal/filesystem/     atomic, symlink-safe writes
internal/updater/        GitHub release check (cached, non-blocking) + self-upgrade
internal/version/        ldflags-injected build metadata
tools/docgen/            regenerates the README command reference (task docs)
test/e2e/                end-to-end tests of the built binary (e2e tag); manifests in testdata/
scripts/bump-version.sh  bumps VERSION, commits and tags (task bump)
VERSION                  current release version, MAJOR.MINOR.PATCH (source of truth)
```

## Architecture rules (do not break)

- `internal/sync`, `internal/config` and `internal/cli` must never import a
  concrete backend, except `internal/cli/root.go`, which registers backends.
- A new backend = new package implementing `backend.Backend` + one
  `Register` call. No changes to `sync`, `config` or existing manifests.
- Resolution must be explicit: zero matches → `backend.NotFoundError`, more
  than one → `backend.AmbiguousError`. Never pick the first result.
- Provider CLIs are never dependencies of the vault-sync package (Homebrew
  cask). A backend declares a `toolinstall.Tool` and resolves it lazily with
  `toolinstall.Find`, so a CLI is only installed when a project uses it.
  `internal/toolinstall` must not import backends.
- The manifest stays provider-independent; only `backend:` may hold provider
  options. Do not add fields without a concrete use case (spec §15).

## Security rules (non-negotiable)

- Never log, print, or put secret contents in error messages. `--verbose`
  logs may include names, sizes and ids only.
- Never pass secret values as process arguments (they show up in `ps`);
  the Bitwarden adapter sends JSON on stdin.
- Never read the bw stdout into error messages (it may contain secrets).
  The same applies to `infisical` stdout (tokens) and Infisical API response
  bodies: only the server's short `message` field may be shown.
- Writes go through `filesystem.WriteFile` (temp file → chmod → fsync →
  rename). Do not write destinations directly.
- Default permissions are `0600`, new directories `0700`.
- Downloaded tools must be verified against the SHA-256 digest GitHub
  publishes for the release asset; never install an unverified binary.
- Do not run `bw` or `infisical` against a real vault from tests or from the
  agent shell. Unit tests use the in-memory fakes in
  `bitwarden/backend_test.go` and `infisical/backend_test.go` (httptest).
  Real-vault tests live behind the `integration` and `e2e` build tags; the
  user runs them, not the agent.
- Do not open `.env`, `*.key` or `*.pem` files in this repo.

## Conventions

- Go ≥ 1.26 (see `go.mod`), standard library first; dependencies are cobra,
  yaml.v3, x/mod, x/term. Ask before adding more.
- Errors: wrap with `%w`, lower-case, no trailing punctuation; user-facing
  errors should say how to fix the problem.
- Tests: table-driven where it helps, `t.TempDir()` for filesystem work, no
  network (use `httptest`).
- Commits: Conventional Commits, see **Commit messages** below.
- Do not commit, tag or push unless the user asks.

## Commit messages

Every commit follows [Conventional Commits 1.0](https://www.conventionalcommits.org/).
`.githooks/commit-msg` rejects anything else, and GoReleaser builds the
GitHub Release notes from these subjects, so they are user-facing text.

```
<type>(<optional scope>)!: <description>

<optional body: what and why, wrapped at 72>

<optional footer, e.g. BREAKING CHANGE: …>
```

- **Types**: `feat` (new user-visible behaviour), `fix` (bug fix), `perf`,
  `refactor`, `docs`, `test`, `build`, `ci`, `style`, `chore`, `revert`.
- **Scope** (optional, lower-case): the area touched, e.g. `bitwarden`,
  `infisical`, `cli`, `config`, `sync`, `updater`, `toolinstall`.
- **Description**: imperative mood, lower-case, no trailing period, ≤ 72
  characters (`feat(infisical): support self-hosted servers`).
- **Breaking changes**: add `!` before the colon (`feat(config)!: rename
  project to folder`) and explain the migration in a `BREAKING CHANGE:`
  footer. They appear first in the release notes.
- One logical change per commit; do not mix a `feat` with unrelated
  refactors.
- Release notes: `feat` → Features, `fix` → Bug fixes, `perf` →
  Performance, `refactor`/`revert` → Other changes. `docs`, `test`, `build`,
  `ci`, `style` and `chore` are left out (unless breaking), so choose the
  type by what users notice, not by which files changed.
- `chore(release): vX.Y.Z` is reserved for `task bump`.

## Releasing

The version lives in `VERSION` (`MAJOR.MINOR.PATCH`, without the
`v`). On `main`, `task bump` (patch, the default), `task bump -- minor` or
`task bump -- major` updates `VERSION`, commits `chore(release): vX.Y.Z` and
creates the annotated tag; pushing it (`git push origin main vX.Y.Z`) makes
`.github/workflows/release.yml` run GoReleaser, which publishes the GitHub
Release with the changelog and updates the cask in
`jordigarciaventura/homebrew-tap`. The workflow fails if the tag does not
match `VERSION`. Never edit `VERSION` or create release tags by hand, and
do not run `task bump` unless the user asks. Details in DEVELOPMENT.md →
"Releasing".
