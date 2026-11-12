# Pebble

Pebble is an **experimental, binary-first version control system** written in Go.
The goal is a practical Git alternative for source code and binary assets. Today,
Pebble supports a guarded **local snapshot/branch workflow** and an experimental
**authenticated remote test server**, not a complete Git replacement. Keep Git
and independent backups while dogfooding.

## What works today

- Typed SHA-256 objects, canonical file trees and immutable ordered-parent commits.
- Whole-working-tree commits, change detection (added/modified/deleted), full log.
- Branch creation/listing (including `feature/name`) and file-restoring checkout.
- Checkout refuses dirty tracked files, untracked/ignored file collisions, unsafe
  paths and corrupt target objects before changing files.
- Explicit single-file recovery from HEAD with `pebble restore <file>`.
- Durable checkout/restore journals and explicit `pebble recover` roll-forward.
- Literal symbolic-link targets/types, observable executable identity, and
  recoverable file/directory transitions without deleting untracked children.
- Integrity verification of every stored object/copy and all refs with `pebble fsck`,
  including unreachable loose/packed objects and complete dependency closure.
- Repository discovery from subdirectories; explicit initialization; writer locks
  and atomic individual metadata/object writes.
- Format2 CLI commits stream text and binaries through ROCK chunking and immutable
  paged manifests; checkout/restore verify staged raw bytes before replacement.
- Store API packs provide checksummed compressed physical storage and rebuildable
  indexes without changing typed IDs, file revisions, or refs.
- Explicit read-only v1 migration into a fresh separate v2 destination, with
  selected-branch history verification and a persistent source-to-v2 ID map.
- Experimental legacy-v1-only `pebbled` server/website and authenticated push/fetch.
  New format2 repositories are explicitly rejected by this protocol.

## What does NOT work yet

- Production remote collaboration: the new v1 server is a **single-repository lab**,
  with a shared read/write token, 32 MiB complete-history transfer limit and no
  incremental/resumable sync. `pull`/merge remain unavailable; use `fetch` and
  explicit checkout. Pebble cannot push to GitHub's Git endpoints.
  The old `internal/remote` prototype is not mounted or used by the new server.
- Merge/combine CLI, Git import/export, clone, staging, diff, branch deletion.
- Format2 remote sync and GC. Streaming storage does not yet establish
  large-binary scalability or improvements over Git.
- Special files and empty directories as standalone snapshot entries.
- Full filesystem metadata preservation: executable identity and symbolic-link
  type/target are supported; ACLs, arbitrary permission bits, ownership and
  timestamps are not preserved. Unsupported materialization fails explicitly.

See [the dogfood evaluation and next priorities](docs/dogfood-readiness.md) and the
[longer-term roadmap](docs/next-git-roadmap.md). Older design/checklist documents
are plans, not evidence of shipped functionality.

The [PB-01 v2 format/protocol contract](docs/contracts/v2.md) and its
[canonical byte fixtures](internal/formatcontract/testdata/v2.json) freeze the
current engine's representation. The [format2 store and CLI](docs/store-v2.md)
implement typed durable object/ref publication, immutable paged file construction,
bounded-memory content-defined ingestion and the seven local repository flows.
The `/v1/` service remains legacy-only; there is no automatic migration or fallback.

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
`init` creates format2 metadata without changing existing working files. It refuses
existing or incomplete `.pebble` data. Local commands reject legacy/unmarked
repositories explicitly; use the separate-destination migration below.
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
without changing history and restores the stored regular-file or link type.
It does not authorize discarding a nonempty directory or later external edits.

Commits include **all non-ignored files**, additions and deletions; there is no
staging area. Unchanged commits are rejected. Status uses committed HEAD, not an
index cache. Untracked files may survive checkout unless they collide with a
target leaf, in which case checkout refuses unless literal bytes and observable
type/mode are identical (then the existing identity is adopted without rewriting).
File/directory transitions retain original leaves and verified empty blocking
directories in the journal; untracked children, including empty subdirectories,
are refused rather than recursively removed.
Create the first commit before creating another branch. `log` follows all ordered
parents (first-parent first), displaying each reachable commit once.

New repositories carry a `filesystem-v1` capability marker. Existing format2
repositories remain readable and support regular/executable history without
automatic upgrade. To deliberately permit link entries, back up the repository
and run `pebble enable-filesystem`. This changes only the durable capability
marker, not objects, IDs, refs or working bytes. It excludes incompatible older
writers and refuses active cooperating writers or pending recovery. This protocol
coordinates PB-09's persistent guards, not arbitrary third-party/older lock
protocols. The marker does not grant Windows link privileges or executable-bit
support: an incapable host refuses checkout/restore before working mutation.
Link target bytes are never dereferenced or normalized. Windows captures the
authoritative substitute target/flags, not its display label. Ambiguous cross-host
target spellings fail explicitly; see the [target contract](docs/contracts/v2.md#3-trees-commits-and-tags).

### Explicit legacy v1 migration

Evaluate this experimental path offline on backed-up disposable copies. The
source must be the exact real v1 root (no ancestor discovery, symlinks or filesystem
aliases). The destination must not exist; its real parent must already exist
outside the source.

```sh
pebble migrate-v1 /disposable/v1 /disposable/new-v2
pebble migrate-v1 /disposable/v1 /disposable/selected-v2 --branch main --branch feature/media --head main
```

Without `--branch`, all branches are selected. Without `--head`, source HEAD is
retained and must be selected. Every selected reachable revision is converted and
verified, including shared ancestors. The destination contains selected HEAD's
committed files and `.pebble/migration-v1.json` with branch and blob/tree/commit
ID mappings. Uncommitted files, index caches, settings and credentials are not
copied. Source metadata, refs, HEAD, working files and locks are never modified.

Unsupported metadata/modes/branches, unsafe paths, corrupt or missing dependencies,
existing destinations and overlap fail explicitly. Only canonical shipped v1
JSON and modes `-rw-r--r--` / `-rwxr-xr-x` are supported, not arbitrary permissions
(including legacy `-rw-rw-rw-`). A single unborn `main` is supported, not other
unborn layouts. Legacy envelopes are bounded to 128 MiB; v2 metadata and graph
bounds also apply, without truncating history.

A verified private sibling stage is installed by exclusive directory rename.
Interruption may leave `.pebble-migrate-*` working material, not an acknowledged
destination. An uncertain OS publication error may leave a complete destination:
preserve it, inspect it and run `fsck`; never overwrite it or remove recovery data
automatically. See [migration details](docs/store-v2.md#pb-08-explicit-verified-v1-migration).


### Ignore rules

`.pebbleignore` supports blank lines, `#` comments, `*.log` globs, `build/`
directory rules, `/root-only.txt` anchoring and `assets/cache/` relative paths.
Patterns without a slash match names at any depth. Use `/` separators, including
on Windows. Rules only exclude untracked files; already tracked files stay
tracked. **This is not Git ignore syntax:** negation (`!`), `**`, escaping and
backslashes are unsupported (negation, `**` and backslashes produce errors).
Review `pebble status` for secrets before committing; `.gitignore` is not read.

### Safety and recovery

- Do not edit files concurrently with commit/checkout/restore/recovery. Locks
  serialize cooperating Pebble processes, not editors. Metadata is locally trusted;
  do not open a malicious `.pebble` directory or mix old/new Pebble writers.
- After interruption, back up the entire directory, run `pebble fsck`, then
  `pebble recover`. Reopening never silently mutates files. Recovery finishes the
  original checkout/restore, not a new command or an automatic rollback.
- Pending `.pebble/worktree-journal` blocks commit, branch, checkout, restore and
  status. Target bytes and intent are durable before working paths change.
  Replaced/removed originals (including restore's discarded edits) stay in
  `old-*` slots until completion; blocking directories use `old-dir-*` slots.
  Do not edit/delete recovery files.
- Recovery checks recorded filesystem identities, bytes, modes and directory
  spellings. Later edits, replacements (even identical bytes), untracked collisions,
  aliases, changed branches, missing/corrupt/unsupported intent or barrier errors
  fail closed. Preserve the journal and files for inspection; do not force recovery.
  POSIX cannot atomically predicate rename on a source inode. A detected final
  rename-window substitution is reversed without replacement where possible;
  intervening entries/permissions can block reversal. The error then identifies
  preserved public/retained versions for operator recovery; HEAD is not advanced.
- New persistent `.pebble/worktree.guard` and `write.guard` use OS process locks,
  released on exit. Never unlink guard files. Legacy `worktree.lock`/`write.lock`
  markers still require manual inspection and confirmation no old writer is live.
  Interrupted initialization also needs manual inspection, not retry/reset.
- Successful completion means files and symbolic HEAD have passed platform
  durability barriers. V2 has no mutable staging index: status derives from HEAD.
  Process-kill tests are not hardware power-loss qualification. Concurrent editors
  remain unsupported: detected ownership/byte changes stop recovery, never force it.

## Remote test service

This service does **not** accept the format2 repositories created by the current
CLI. Push/fetch remain available only for existing legacy repositories; the seven
local commands do not fall back to the legacy engine. Use the isolated legacy
fixture tests for service development, not a format2 working repository.

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
go test ./cmd/pebble -run '^TestLegacyRemoteCLI$' -count=1
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

See [disposable fixtures and measured shipped-behavior baselines](docs/testing-fixtures.md)
for corpus sizes, compatibility/fault coverage, reproduction commands and measurement gaps.

CI exercises Windows, Linux and macOS, the minimum and stable Go toolchains, and
Linux race detection. Contributions should include regression tests, especially
for data preservation and corruption handling.
