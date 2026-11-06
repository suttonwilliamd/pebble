# Pebble

Pebble is an **experimental, binary-first version control system** written in Go.
The goal is a practical Git alternative for source code and binary assets. Today,
Pebble supports a guarded **local snapshot/branch workflow** and an experimental
**authenticated remote test server**, not a complete Git replacement. Keep Git
and independent backups while dogfooding.

## What works today

- SHA-256-addressed blobs, deterministic file trees and linked immutable commits.
- Whole-working-tree commits, change detection (added/modified/deleted), full log.
- Branch creation/listing (including `feature/name`) and file-restoring checkout.
- Checkout refuses dirty tracked files, untracked/ignored file collisions, unsafe
  paths and corrupt target objects before changing files.
- Explicit single-file recovery from HEAD with `pebble restore <file>`.
- Object integrity verification with `pebble fsck`.
- Portable verified local backups: `bundle create` exports complete HEAD history;
  `bundle import` restores it into a new branch without changing HEAD or files.
- Repository discovery from subdirectories; explicit initialization; writer locks
  and atomic individual metadata/object writes.
- Experimental `pebbled` server/website, authenticated push/fetch, complete typed
  history transfer and fast-forward compare-and-swap updates. Docker Compose
  deployment defaults to loopback-only with a dedicated data volume.
- ROCK chunking/deduplication libraries, with bounded-memory streaming and
  batch/stream equivalence tests. **ROCK is not yet wired into CLI commits.**

## What does NOT work yet

- Production remote collaboration: the new v1 server is a **single-repository lab**,
  with a shared read/write token, 32 MiB complete-history transfer limit and no
  incremental/resumable sync. `pull`/merge remain unavailable; use `fetch` and
  explicit checkout. Pebble cannot push to GitHub's Git endpoints.
  The old `internal/remote` prototype is not mounted or used by the new server.
- Merge/combine CLI, Git import/export, clone, staging, diff, branch deletion.
- Chunk-based CLI snapshots, large-file streaming storage, compression or GC.
  CLI blobs currently load whole files and store base64 content in JSON. Do not
  expect large-binary scalability or storage improvements over Git yet.
- Symlinks/special files and empty directories in snapshots. Non-ignored symlinks
  and special files cause an error rather than being silently lost.
- Crash-atomic checkout: ordinary I/O errors trigger a best-effort file-content
  rollback, but power loss or process termination can interrupt checkout.
- Full filesystem metadata preservation: executable bit on Unix is supported;
  ACLs, arbitrary permission bits, ownership and timestamps are not preserved.

See [the dogfood evaluation and next priorities](docs/dogfood-readiness.md) and the
[longer-term roadmap](docs/next-git-roadmap.md). Older design/checklist documents
are plans, not evidence of shipped functionality.

## Install

Requires Go 1.21 or newer. Git is needed only to download/update this source.
The SQLite dependency is pure Go; no separate SQLite installation is required.
On macOS 26+, use the current Go toolchain: Go 1.21's linker cannot produce all
binaries accepted by the newer OS. Minimum-toolchain macOS CI uses macOS 15.

```sh
git clone https://github.com/suttonwilliamd/pebble.git
cd pebble
go test ./...
go build -o pebble ./cmd/pebble  # Windows: -o pebble.exe
```

Alternatively, `go install github.com/suttonwilliamd/pebble/cmd/pebble@latest`
installs into your Go bin directory. Add it to PATH.

## Dogfood safely

Start in a disposable copy or a small project that remains backed up with Git.
Do not delete `.git`; add `.pebble/` to that project's `.gitignore` if using both.
Pebble always excludes `.git` and `.pebble` metadata from snapshots.

Set your identity (required; no more placeholder author):

```powershell
$env:PEBBLE_AUTHOR = "Your Name"
$env:PEBBLE_EMAIL = "you@example.com"
```

On POSIX shells, use `export PEBBLE_AUTHOR="Your Name"` and
`export PEBBLE_EMAIL="you@example.com"` instead.

```sh
pebble init
pebble status
pebble commit -m "Initial snapshot"
pebble branch experiment
pebble checkout experiment
# edit files
pebble status
pebble commit "Try a change"
pebble log
pebble checkout main
pebble fsck
```

To undo edits or recover a deleted tracked file, run `pebble restore path/to/file`.
**This explicitly discards that file's uncommitted contents.** It restores HEAD
without changing history and refuses untracked paths, directories and symlinks.

Commits include **all non-ignored files**, additions and deletions; there is no
staging area. Unchanged commits are rejected. Status uses committed HEAD, not an
index cache. Untracked files may survive checkout unless they collide with a
target file, in which case checkout refuses unless contents and, on Unix,
executable mode are identical (then the existing file is adopted without rewriting).
File/directory transitions are currently rejected; empty directories left by
checkout are harmless.

Windows cannot observe Unix executable bits. Tracked files retain their committed
mode metadata through status checks and later content commits, including files
subsequently covered by ignore rules. Imported executable files do not appear
modified merely because of the host platform. New Windows files are non-executable;
change executable permissions on Unix, where real mode changes remain tracked.

### Ignore rules

`.pebbleignore` supports blank lines, `#` comments, `*.log` globs, `build/`
directory rules, `/root-only.txt` anchoring and `assets/cache/` relative paths.
Patterns without a slash match names at any depth. Use `/` separators, including
on Windows. Rules only exclude untracked files; already tracked files stay
tracked. **This is not Git ignore syntax:** negation (`!`), `**`, escaping and
backslashes are unsupported (negation, `**` and backslashes produce errors).
Review `pebble status` for secrets before committing; `.gitignore` is not read.

### Safety and recovery

- Do not edit files concurrently with commit/checkout. The lock serializes Pebble
  writers, not editors or other tools. Repository metadata is assumed locally
  trusted; do not open a malicious `.pebble` directory.
- If an interrupted process leaves `.pebble/write.lock`, first ensure no Pebble
  writer is running, then remove that lock manually. Never remove an active lock.
- After interruption, back up the entire directory and run `pebble fsck`.
  Committed objects remain available; automatic checkout recovery is not yet built.
- Legacy snapshots containing Windows backslash paths are not portable to the
  new guarded checkout. Keep the original working copy and Git backup; there is
  no automatic migration yet.

### Portable local backup and restore

Use a **new filename outside the working tree**, then copy the resulting file to
independent storage. Bundles contain committed file contents and author metadata;
protect them as carefully as the original repository. They are not encrypted.

```sh
# In the source repository (also works from a subdirectory):
pebble fsck
pebble bundle create ../project-backup.bundle
# Verify a transferred copy before importing; no repository is required:
pebble bundle inspect ../project-backup.bundle

# Transfer project-backup.bundle to another machine/directory.
# In a disposable destination directory:
pebble init
pebble bundle import /path/to/project-backup.bundle recovered/main
pebble branch
pebble fsck
# Import leaves HEAD, existing branches and working files unchanged.
# Inspect/recover explicitly; normal dirty-file/collision checkout guards apply:
pebble checkout recovered/main
pebble log
```

`pebble bundle help` and `pebble bundle inspect <file>` require no repository.
Inspection verifies the same complete typed graph before printing a JSON summary:
`version`, `source_ref`, `tip`, `object_count`, `commit_count`, `tree_count` and
`blob_count`. Counts are unique immutable objects, not per-commit file totals.
No file contents, objects or branches are written, no writer lock is acquired, and
invalid input exits unsuccessfully without a partial success summary. This proves
content integrity, not the backup's author or authenticity.
Paths are relative to the invocation directory, not the discovered repository
root. Create refuses existing files (including symlinks) and destinations directly
inside this repository's `.pebble`/`.git` metadata. Import requires an initialized
repository and a **new branch**, including when an existing branch has no commits;
it never updates or switches the current branch. Nested names such as
`recovered/main` are supported, with existing portable ref/case-collision checks.

**Version 1 format and limits:** a single UTF-8 JSON document using Pebble's
existing typed transfer bundle (`version`, source `ref`, tip `hash`, `objects`).
Object content is base64-encoded; the **entire encoded file, including whitespace,
must be at most 32 MiB (33,554,432 bytes)** and contain at most **50,000 objects**.
Only the current HEAD's complete reachable commit/parent/tree/blob graph is
exported: no other branch tips, index, ignored/untracked files or uncommitted edits.
An unborn branch has no history to back up and is refused; a committed empty tree
is valid. All commit bytes, authors/emails, timestamps, messages, parent links,
tree modes and binary blobs retain their original addresses. Executable modes are
preserved across Windows checkouts and subsequent commits; executable checkout
and mode changes apply on Unix only. This does not add symlink, ACL or arbitrary
filesystem-metadata support.

Create verifies every reachable object before exclusively creating and syncing
the file. Import and inspection share bounded input decoding and enforce the
object count before allocating an excess entry. Both reject unsupported versions,
unknown fields, duplicate top-level fields, extra documents, invalid paths/refs/types,
duplicate/unreachable objects, missing dependencies and hash/size corruption.
Import validates **before storing any objects**; inspection never stores anything.
Only regular files are accepted:
pre-open checks follow aliases to reject FIFOs/devices without blocking, and the
opened descriptor is checked again. The input directory must be trusted and not
concurrently replaced between these checks and opening; this is not protection
against an attacker racing filesystem replacements.
Hashes provide integrity, not proof of a backup's author or authenticity. The
format remains whole-history/in-memory, without compression, streaming large-file
storage, incremental bundles or resumable transfers.

The writer lock protects the source HEAD and destination ref publication.
Import stores validated immutable objects before atomically publishing the new
ref; a later I/O failure may leave unreachable objects but never switches HEAD or
edits working files. Output files use exclusive creation, file sync and checked
close, not a crash-atomic publication transaction: abrupt termination can leave a
partial bundle, and directory-entry durability is filesystem-dependent. Keep an
earlier independent backup until a transferred copy imports and passes `fsck`;
truncated bundles are rejected, not automatically repaired.

This is a local escape hatch, **not Git import/export (issue #3)**. GitHub's Git
endpoints still cannot consume Pebble bundles.

## Remote test service

See [the Garfield deployment and remote test runbook](docs/garfield-deployment.md).
It includes the Docker Compose stack, token handling, SSH forwarding, a two-client
acceptance test, optional Cloudflare considerations and rollback boundaries.
Garfield is deployed **loopback-only**, with live sync, restart and independent
backup/restore checks passed. Its SSH policy disables TCP forwarding; workstation
access awaits an approved ingress route. No public hostname has been added.

For a local test server, build `go build -o bin/pebbled ./cmd/pebbled`, generate a
private strong token file outside the repository and set `PEBBLE_TOKEN_FILE`.
Start `bin/pebbled -data /path/to/disposable-data` (Windows: use `.exe`). It listens
on `127.0.0.1:7931` by default and shows a small static website at `/`.

```sh
pebble push http://127.0.0.1:7931          # push current HEAD, fast-forward only
pebble fetch http://127.0.0.1:7931 main   # import incoming/main; files unchanged
pebble checkout incoming/main
pebble branch work
pebble checkout work                    # do not develop on incoming/main
# edit, commit
pebble push http://127.0.0.1:7931 main   # publish work HEAD to remote main
```

The CLI requires HTTPS unless connecting to loopback (including an SSH forward).
Never expose the bare HTTP listener directly to the Internet or put tokens in
URLs, CLI arguments, source files or chat.

## Development

```sh
go test ./...
go vet ./...
go test ./internal/rock -run '^$' -fuzz FuzzStreamRoundTrip -fuzztime 20s
```

CI exercises Windows, Linux and macOS, the minimum and stable Go toolchains, and
Linux race detection. Contributions should include regression tests, especially
for data preservation and corruption handling.
