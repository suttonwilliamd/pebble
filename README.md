# Pebble

Pebble is an **experimental, binary-first version control system** written in Go.
The goal is a practical Git alternative for source code and binary assets. Today,
Pebble supports a guarded **local snapshot/branch workflow**, not a complete Git
replacement. Keep Git and independent backups while dogfooding.

## What works today

- SHA-256-addressed blobs, deterministic file trees and linked immutable commits.
- Whole-working-tree commits, change detection (added/modified/deleted), full log.
- Branch creation/listing (including `feature/name`) and file-restoring checkout.
- Checkout refuses dirty tracked files, untracked/ignored file collisions, unsafe
  paths and corrupt target objects before changing files.
- Explicit single-file recovery from HEAD with `pebble restore <file>`.
- Object integrity verification with `pebble fsck`.
- Repository discovery from subdirectories; explicit initialization; writer locks
  and atomic individual metadata/object writes.
- ROCK chunking/deduplication libraries, with bounded-memory streaming and
  batch/stream equivalence tests. **ROCK is not yet wired into CLI commits.**

## What does NOT work yet

- Remote collaboration: CLI `push`/`pull` deliberately fail. The prototype client
  does not yet provide complete history transfer, safe ref negotiation or a
  production server. Pebble cannot push to GitHub's Git endpoints.
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
target file, in which case checkout refuses. File/directory transitions are
currently rejected; empty directories left by checkout are harmless.

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

## Development

```sh
go test ./...
go vet ./...
go test ./internal/rock -run '^$' -fuzz FuzzStreamRoundTrip -fuzztime 20s
```

CI exercises Windows, Linux and macOS, the minimum and stable Go toolchains, and
Linux race detection. Contributions should include regression tests, especially
for data preservation and corruption handling.
