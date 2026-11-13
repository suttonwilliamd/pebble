# Pebble

Pebble is an **experimental, binary-first version control system** written in Go.
The goal is a practical Git alternative for source code and binary assets. Today,
Pebble supports a guarded **local snapshot/branch workflow** and an experimental
**authenticated remote test server**, not a complete Git replacement. Keep Git
and independent backups while dogfooding.

## What works today

- Typed SHA-256 objects, canonical file trees and immutable ordered-parent commits.
- Path-selective immutable staging, explicit tracked `commit --all`, two-column
  staged/unstaged status, config-backed author/committer identity, full log.
- Branch creation/listing (including `feature/name`) and file-restoring checkout.
- Checkout refuses dirty tracked files, untracked/ignored file collisions, unsafe
  paths and corrupt target objects before changing files.
- Explicit single-file recovery from HEAD with `pebble restore <file>`.
- Durable checkout/restore journals and explicit `pebble recover`, including
  automatic no-clobber abort/reconciliation with discoverable preserved versions.
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
- Merge/combine CLI, Git import/export, clone, diff, branch deletion.
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

Set your identity (required; no inferred host/account or placeholder identity):

```sh
pebble config --global user.name "Your Name"
pebble config --global user.email "you@example.com"
```

Or set `PEBBLE_AUTHOR` and `PEBBLE_EMAIL` for both identities (PowerShell:
`$env:PEBBLE_AUTHOR = "Your Name"` / `$env:PEBBLE_EMAIL = "you@example.com"`).
Configuration is Pebble-only: global `$HOME/.pebbleconfig` (falling back to the
OS home if `HOME` is unset), local `.pebble/config.json`. `pebble config` defaults
to local; `--global` works outside a repository. Reading a key prints that scope's
stored value. Files are bounded JSON maps of `user.name`, `user.email`,
`author.name`, `author.email`, `committer.name`, `committer.email`; unknown keys,
duplicates, linked/malformed files and invalid identities fail closed.
The 64 KiB limit applies to the complete serialized config, including JSON escaping
and its final newline. Oversized updates leave the prior config file unchanged.

Precedence **per field**, lowest to highest:

1. Global config, overlaid by local config for the same key.
2. `author.*` / `committer.*` override effective `user.*` for their role.
3. `PEBBLE_AUTHOR` / `PEBBLE_EMAIL` override both roles (legacy common identity).
4. `PEBBLE_AUTHOR_NAME` / `PEBBLE_AUTHOR_EMAIL` and
   `PEBBLE_COMMITTER_NAME` / `PEBBLE_COMMITTER_EMAIL` override their own role.

An explicitly empty environment value is an error, not a fallback. New commits
record both roles with the operation's native timestamp; old commits without
committer metadata remain unchanged and do not display an invented committer.
Git identity/configuration, filters, attributes, autocrlf and hooks are not read.

```sh
pebble init
pebble status
pebble add .                       # or: pebble add src/file assets/model.bin
pebble commit -m "Initial snapshot"
pebble branch experiment
pebble checkout experiment
# edit files
pebble status
pebble add path/to/changed-file
pebble commit "Try a change"
pebble log
pebble checkout main
pebble fsck
```

To undo edits or recover a deleted tracked file, run `pebble restore path/to/file`.
**This explicitly discards that file's uncommitted contents.** It restores HEAD
without changing history and restores the stored regular-file or link type.
It does not authorize discarding a nonempty directory or later external edits.

Default commits contain **exactly staged revisions**, not later working edits.
`add` captures file bytes/type/executable identity in immutable objects and stages
selected deletions. Paths are literal, relative to the invoking directory; a
directory includes its descendants, `.` selects that directory, and `--` precedes
paths beginning with `-`. Ignored untracked paths are not added; already indexed
paths stay tracked. No bytes are decoded, filtered or line-ending normalized.

`commit --all [-m] <message>` (or `-a`) explicitly refreshes **index-tracked**
modifications/deletions, including staged additions, then commits that tree.
It does not add unrelated untracked files, ignored new files, or an unstaged
re-add after a staged deletion. Use `add` explicitly to track those re-adds.
Unchanged commits are rejected; an explicitly staged empty initial tree remains
supported. `status` prints Git-style `XY path`: X = HEAD versus index, Y = index
versus worktree; `??` = untracked. `MM` means a staged revision plus later edits,
`AM` a staged addition plus later edits. A staged deletion and untracked re-add
appear separately. `status --staged` / `--unstaged` select the respective side.

Staging publishes the `staging-v1` capability marker before index facts, excluding
incompatible older writers; it preserves the existing filesystem capability.
Missing/malformed/stale indexes fail closed. Do not remove or edit `.pebble/index`
to recover: preserve metadata and history. A prepared commit pins exact HEAD/base/
tree/next IDs, so reopen recognizes publication without duplicating staged work.
Checkout conservatively refuses staged changes. `restore <file>` still restores
HEAD working bytes and leaves staged revisions intact; it is not an unstage command.

Untracked files may survive checkout unless they collide with a
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

Pebble reads `.gitignore` and `.pebbleignore` in each reachable directory using
Git-style patterns: ordered `!` negation, escaped `#`, `!` and spaces, `*`, `?`,
character classes, `**`, `/root-only` anchoring, basename and directory-only rules.
Use `/` separators on every platform. Blank lines and unescaped leading `#` are
comments; unescaped trailing spaces are discarded. Matching is case-sensitive.

Rules layer from the root toward the path: **deeper directory rules override
ancestor rules; `.pebbleignore` follows `.gitignore` at the same directory; the
last applicable match wins**. A negation cannot reinclude a file while its parent
directory remains excluded, and rules inside excluded directories are not read.
For example, use `cache/*` and `!cache/keep/` rather than `cache/` and
`!cache/keep/file` to reopen descendants.

Already tracked files stay tracked, including modifications and deletions beneath
newly ignored directories. `.pebble` (including recovery archives/preimages) and
`.git` components remain protected regardless of negation. Rule files must be
ordinary files: linked/replaced/unsafe rule files fail closed, never fall back to
dereferencing external data.

`pebble check-ignore <path>...` prints excluded untracked paths; exit status is 0
if any are excluded, 1 otherwise. `-v` / `--verbose` prints
`source:line:pattern<TAB>path<TAB>ignored|included|tracked|unmatched` for each path,
including the winning negation or excluded-parent rule. Verbose status is 0 if a
rule matches (including a negation), 1 otherwise. Tracked paths have no ignore
rule; protected metadata uses `<metadata>:0`. Paths resolve from the current
directory, must stay inside the repository and retain Pebble's portable-path
checks. Use `--` before paths beginning with `-`.

Differences from Git: `.pebbleignore` is an additional per-directory layer;
personal/global excludes and `.git/info/exclude` are not read; metadata protection
is unconditional and case-insensitive; rule-file links are errors rather than
silently skipped; nonportable worktree paths are still rejected. Verbose output
adds explicit result labels and tracked/unmatched rows, rather than Git's silent
default for those rows. This is local ignore compatibility, not Git index/staging
support. Review `pebble status` for secrets before committing.

### Safety and recovery

- Do not edit files concurrently with commit/checkout/restore/recovery. Locks
  serialize cooperating Pebble processes, not editors. Metadata is locally trusted;
  do not open a malicious `.pebble` directory or mix old/new Pebble writers.
- After interruption, back up the entire directory, run `pebble fsck`, then
  `pebble recover`. Reopening is read-only. An unchanged valid transaction rolls
  forward; later public edits instead trigger automatic safe abort/reconciliation.
  **An aborted checkout/restore is not successful target publication.**
- Pending `.pebble/worktree-journal` blocks commit, branch, checkout, restore and
  status. Version-3 journals durably copy leaves the engine may move (including
  restore's dirty contents) before mutation; native originals and empty blocking
  directories are also retained during moves. Do not edit/delete active recovery
  material.
- Recovery leaves newer public entries untouched. It returns displaced originals
  to vacant, identity-verified canonical names without replacement. If a newer
  entry or missing/rebound parent owns that namespace, it automatically preserves
  alternate versions under `.pebble/recovery/archives/<transaction>/`.
  Durable receipts under `.pebble/recovery/receipts/` record exact paths, prior and
  requested commit IDs and the abort reason. Every `pebble recover`, including
  after process death following intent retirement, prints those paths and the
  aborted-operation result. No manual filename move is needed to clear an abort.
  Archives use existing metadata exclusion, not new user-path ignore rules.
- Required repair or durability writes denied by permissions/inaccessible roots
  remain pending with the actual OS error; recovery never chmods, escalates or
  spins. Restore access and rerun `recover`; it resumes idempotently without
  overwriting intervening edits. Corrupt/unsupported records, changed history
  pins or lost legacy preimages likewise never become false success.
  Supported old v1/v2 replay remains available. An explicit conflicting recovery
  preserves the exact old envelope before a local v3 abort checkpoint. Missing
  originals may be reconstructed from matching pinned history; unknown old dirty
  bytes overwritten before old code saved them cannot be recreated.
- POSIX cannot predicate rename on a source inode. A final-window substitution
  may temporarily move a public name before detection. Exclusive reversal and
  automatic reconciliation preserve versions, but do not make arbitrary external
  namespace edits or all paths plus HEAD atomic. A newer canonical entry is never
  replaced to pretend the original name was restored.
- New persistent `.pebble/worktree.guard` and `write.guard` use OS process locks,
  released on exit. Never unlink guard files. Legacy `worktree.lock`/`write.lock`
  markers still require manual inspection and confirmation no old writer is live.
  Interrupted initialization also needs manual inspection, not retry/reset.
- Successful completion means files and symbolic HEAD have passed platform
  durability barriers. Staging metadata uses the same guarded atomic publication
  barriers; checkout starts only with a clean index.
  Process-kill tests are not hardware power-loss qualification. Keep editors
  quiescent; no force command or unconditional concurrent-editor guarantee exists.

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
