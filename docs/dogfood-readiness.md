# Dogfood readiness evaluation

## Verdict

Suitable for **experimental local/remote tests on backed-up small projects**, not as the
sole copy of valuable work or a team Git replacement. The baseline tests passed,
but the original CLI was much less complete than its README claimed. There were
no CLI/snapshot tests covering the basic user workflow.

## Remote follow-up

A new authenticated `pebbled` test server, static website and guarded CLI
push/fetch now transfer complete typed history, verify hashes and enforce
fast-forward compare-and-swap updates. Fetch never changes working files and
checks HEAD under the writer lock before publishing its incoming branch.
`pull`/merge remain disabled. The legacy remote prototype is not used.

The protocol is bounded to 32 MiB JSON and 50,000 objects; it is not incremental
or suitable for large binary repositories. Go 1.21/current tests and an actual
local two-client server-process roundtrip (including binary hashes, stale-push
rejection and restart persistence) passed. CI additionally exercises Docker
Compose with non-root secret access, a persistent volume and container restart.

See [the deployment runbook](garfield-deployment.md). Garfield now runs an isolated
loopback-only stack. Independent client repositories on Garfield passed complete
history/1 MiB binary roundtrips, stale-push rejection, auth/invalid-bundle checks
and restart persistence. A stopped-volume backup was restored into a separate
volume/container and matched history and bytes; an off-host copy was hash checked.
Existing containers were not restarted. No DNS/tunnel modifications were made.

Garfield explicitly disables SSH TCP forwarding; that policy remains unchanged.
Workstation-to-server API testing awaits an approved ingress route. Scheduled,
encrypted off-host backups and retention remain unimplemented.

## Blockers addressed in the original local checkpoint

| Finding | Change |
| --- | --- |
| Opening a directory silently created repository metadata | Open is read-only, searches ancestors, requires explicit init |
| Reinitialization could reset HEAD/main | Init refuses existing metadata |
| Commit ignored blob/tree/index storage failures | Checked object writes; publish ref only after objects succeed |
| Placeholder commit identity | Required PEBBLE_AUTHOR / PEBBLE_EMAIL |
| Ignore file parser did nothing | Documented glob subset, errors for unsupported syntax, metadata always excluded |
| Status printed an index hash and hardcoded main | Compare filesystem against HEAD; real branch and A/M/D output |
| Log showed one commit | Walk full parent chain with cycle protection |
| Branch creation overwrote existing branches | Validation, duplicate rejection, nested branch listing |
| Checkout changed HEAD without restoring any file | Restore target files, delete old tracked files, protect dirty/untracked work |
| Invalid hashes could panic or construct unsafe object paths | Strict SHA-256 validation and object content/size/address checks |
| Unchecked refs could escape repository paths | Validate branch reference components |
| Metadata writes could truncate files | Temp-file/sync/rename individual writes; cooperating-writer lock |
| Push sent only one commit; pull treated every object as a blob | Disable CLI sync with an actionable explanation, keep prototype library |
| Streaming chunker discarded its remaining bytes at EOF | Bounded buffering, shared batch boundaries, tail flush, invalid-config checks |
| Error Is method failed go vet / standard error matching | Standard errors.Is support with code targets |

A follow-up adds `pebble restore <file>` for explicit single-file recovery from
HEAD, with corruption checks before overwriting and refusal of untracked paths,
directories and symlinks. CI exposed Go 1.21's incompatibility with macOS 26's
Mach-O loader; minimum-toolchain macOS testing is pinned to macOS 15, while the
stable toolchain is tested on the latest macOS.

Regression tests cover local workflow round trips, complete CLI history, nested
repository discovery, ignored/previously tracked files, duplicate branches,
corruption preflight, untracked collisions, unsafe refs/paths, writer locking,
object tampering and streaming reconstruction/boundary equivalence. Symlink tests
run where OS permissions allow them.

## Local portable backup checkpoint

`pebble bundle create <file>` now backs up the complete committed history reachable
from current HEAD. `pebble bundle import <file> <new-branch>` validates the entire
typed graph, stores objects, then publishes only a new branch under the existing
writer lock. Import leaves HEAD, existing branches, index and working files
unchanged, even with dirty tracked files. Recovery requires a separate explicit
checkout, retaining the normal dirty-work/untracked-collision safeguards.
`pebble bundle inspect <file>` verifies a backup without any repository and emits
only a JSON summary after full validation: version, original source ref, tip and
unique commit/tree/blob/total object counts. It does not acquire a writer lock or
write objects, refs, working files or input bytes.

```sh
# Source: choose a new file outside the working tree.
pebble bundle create ../project-backup.bundle
# Verify the transferred file from any directory, even outside a repository:
pebble bundle inspect /path/to/project-backup.bundle
# Then, in an initialized destination:
pebble bundle import /path/to/project-backup.bundle recovered/main
pebble fsck
pebble checkout recovered/main
pebble log
```

The version-1 file is the existing complete-history JSON transfer bundle, with
base64 object contents. Limits are **32 MiB (33,554,432 bytes) for the entire file**
and **50,000 objects**. Neither command overwrites its target: create exclusively
creates a new file; import refuses existing empty or populated branches and ref
case/file-directory collisions. Unborn history is refused without creating a
file, while committed empty snapshots are valid. Other branch tips and working
edits are not backed up. Authors, emails, timestamps, messages, parents, tree
modes and binary bytes remain content-address-identical; Unix executable bits
survive roundtrip, while Windows retains the mode in metadata only.

Final checks on Windows with Go 1.27.3 passed after independent review:
`go test ./... -count=1` (eight tested packages; `cmd/pebbled` has no tests),
`go vet ./...`, and `go build ./...`. Targeted bundle tests also passed.
New behavior tests cover full binary history roundtrip and older-snapshot
checkout, metadata/modes, preserved dirty work/HEAD/refs, locks, no overwrites,
malformed/tampered/truncated/incomplete graphs, version/object-count limits and
the exact encoded 32 MiB boundary.

Independent review identified allocation amplification from decoding an entire
object array before checking its count, and a Unix FIFO that could block on open
before its type was checked. Import now decodes entries individually and stops
before object 50,001; guard-reader regressions prove early abort without reading
the hostile excess value or expanding trailing arrays. It checks regular-file
type before open (following aliases) and again on the descriptor. Input directories
must remain trusted and must not be concurrently replaced between those checks.
A five-second child-process FIFO/alias regression is included for supported Unix
hosts; it was explicitly skipped on this Windows validation host, not claimed as
executed Unix proof.

An actual compiled CLI smoke used independent disposable source/destination
repositories, three commits/two authors and two versions of a 1 MiB binary.
The post-review 2,800,840-byte backup was copied, imported and explicitly checked out;
complete log and typed object contents matched, and both old/new binary SHA-256
hashes matched. Source `fsck` verified 12 objects; destination `fsck` verified 15
(including its independent local history). Import preserved HEAD/current ref,
the absent index, dirty tracked bytes and untracked binary bytes. Existing-file/
branch overwrites, truncated/tampered bundles and dirty checkout were rejected.
The rebuilt CLI also rejected a malformed 50,001st object before parsing it and
rejected a trailing hostile array; invalid inputs left the repository unchanged.

The read-only inspection follow-up passed the complete Go tests/vet/build on
Windows with Go 1.27.3. Behavior regressions cover deduplicated tree/blob counts,
full integrity rejection before any summary, JSON CLI output outside a repository,
no metadata creation and input/source preservation. The shared FIFO/alias
regression also covers inspection but remains unexecuted on Windows.
A rebuilt CLI independently inspected a three-commit, three-tree, four-blob backup
from a directory containing no repository: exact tip/counts matched, source dirty
data and every input/sentinel digest remained unchanged. Truncated, tampered and
excess-object inputs exited unsuccessfully with empty stdout. Inspection does not
replace testing an explicit import/checkout or keeping independent backups.

Remaining limits: bounded whole-history/in-memory JSON, not compression or
incremental/streaming backups; no encryption/authenticity, Git interoperability,
chunk format, merge, server changes or automatic recovery. Synced exclusive
output is not crash-atomic publication: an interrupted create may leave a partial
file that import rejects, and directory durability depends on the filesystem.
An I/O failure while importing validated objects can leave unreachable objects;
the branch is published only after all object writes succeed. Keep independent
backups until a transferred bundle imports and passes `fsck`.

## Next implementation priorities

1. **Recoverable checkout transactions.** Durable journal, startup recovery and
   injected failures at every filesystem step. Cover full disks, permission errors,
   cancellation, file/directory transitions, case collisions and interrupted writes.
   Current rollback is best effort, not a durability guarantee.
2. **Wire ROCK into the snapshot format.** Versioned file manifests referencing
   chunks, streaming reads/writes, hash/type domain separation and a migration plan.
   Measure binary edits, insertion-heavy workloads, peak RSS and storage amplification.
   Do not market binary efficiency before benchmarking the actual CLI path.
3. **Git interoperability after local backup/restore.** Portable verified local
   bundle export/import is implemented above. Git import/export preserving authors,
   parents and modes remains open (issue #3); this checkpoint does not implement it.
4. **Complete sync graph and server.** Transfer all reachable commits/trees/blobs
   or manifests/chunks, validate before publication, fast-forward/CAS ref updates,
   protocol versioning, authentication, size limits and interrupted-transfer recovery.
5. **Daily ergonomics.** Read-only text/binary diff, restore from arbitrary revisions, config-backed
   identity, safe branch deletion, ignore tooling and signed release binaries.
6. **Merge and binary conflict UX.** Connect the existing combine library only
   after preservation tests and explicit resolution/recovery semantics are in place.

## Known constraints

Whole-file JSON/base64 storage and hashing still consume memory proportional to
file size. No staging, merge CLI, GC, automatic migration or durable checkout
journal. Remote sync and local bundles remain bounded whole-history transfers.
No symlinks, special files, empty directory snapshots or full
permission/ACL preservation. Local metadata must be trusted. Do not run editors
or external filesystem mutations concurrently with a snapshot/checkout.

The original README comparison that Git "stores full snapshots" and therefore
necessarily bloats binaries was misleading: Git models snapshots but also uses
packfile compression and deltas. Pebble's intended benefits need comparative
benchmarks, not assumptions about Git's logical data model.
