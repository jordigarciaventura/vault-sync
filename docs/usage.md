# Sync behavior

[Back to README](../README.md) · [Command reference](commands.md)

Terminal output uses green for created or pushed files, yellow for updates,
muted gray with `-` for unchanged files, cyan for dry runs, and red for failures.
Status text remains visible without color. Colors are disabled when output is
redirected, `TERM=dumb`, or `NO_COLOR` is non-empty.

`pull` writes every file atomically (temporary file in the same directory →
permissions → fsync → rename), creates missing directories with `0700`,
reports whether each file was created, updated or unchanged, and never deletes
files. Before replacing a file whose contents differ, it preserves the original
beside it as `<filename>.<UTC timestamp>.bak` (for example,
`.env.20261124T120000.123456789Z.bak`), retaining its permissions. Name
collisions get a numeric suffix so previous backups are never overwritten.
Unchanged files create no backup. Backups contain secrets too; keep them out
of version control and remove them when no longer needed. If a backup cannot
be created, the original file is left untouched and that file fails.
If one file fails, the others are still processed and the command exits
with status `1`. `--dry-run` checks that every remote resource exists and
reports what would be written, without downloading attachments or touching the
filesystem.
