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

A follow-up adds `pebble restore <file>` for explicit single-leaf recovery from
HEAD. PB-10 preserves stored regular-file/link type and refuses directory
descendant deletion or later conflicting edits. Minimum-toolchain macOS testing
is pinned to macOS 15 for Go 1.21's loader compatibility; stable uses latest macOS.

Regression tests cover local workflow round trips, complete CLI history, nested
repository discovery, ignored/previously tracked files, duplicate branches,
corruption preflight, untracked collisions, unsafe refs/paths, writer locking,
object tampering and streaming reconstruction/boundary equivalence. Symlink tests
run where OS permissions allow them.

Exhaustive fault/process-boundary matrices freeze one generated, quiescent
two-commit fixture and copy it into independent roots with new files/inodes for
each case. This avoids repeatedly publishing identical setup history; transaction
calls, durable barriers, every discovered fault seam and recovery assertions
remain real and unchanged. No hard links, shared writable fixture files, reduced
case lists or increased CI timeout are used. Complete package tests execute
serially to avoid overlapping CLI/repository/store filesystem-heavy workloads.

## Next implementation priorities

1. **Recoverable checkout qualification.** PB-09 adds durable roll-forward intent
   and explicit `pebble recover`, with filesystem faults, process interruption,
   cancellation and external-edit preservation regressions. PB-10 adds guarded
   file/directory/link transitions; physical power-loss qualification is outstanding.
2. **Wire ROCK into the snapshot format.** Versioned file manifests referencing
   chunks, streaming reads/writes, hash/type domain separation and a migration plan.
   Measure binary edits, insertion-heavy workloads, peak RSS and storage amplification.
   Do not market binary efficiency before benchmarking the actual CLI path.
3. **Local backup/restore and Git interoperability.** Portable verified bundle
   export/import, then Git import/export preserving authors, parents and modes.
   This is the shortest route to practical escape hatches and GitHub-backed use.
4. **Complete sync graph and server.** Transfer all reachable commits/trees/blobs
   or manifests/chunks, validate before publication, fast-forward/CAS ref updates,
   protocol versioning, authentication, size limits and interrupted-transfer recovery.
5. **Daily ergonomics.** Read-only text/binary diff, restore from arbitrary revisions,
   safe branch deletion and signed release binaries. PB-12 adds selective staging,
   staged/unstaged status and config/environment author/committer identity.
6. **Merge and binary conflict UX.** Connect the existing combine library only
   after preservation tests and explicit resolution/recovery semantics are in place.

## Known constraints

Format2 local commands use streaming chunks/manifests and checksummed packs;
legacy v1 remains whole-file JSON/base64. No merge CLI or format2 remotes,
GC or automatic migration. Checkout/restore have explicit journal recovery;
they are not a guarantee against concurrent editors or arbitrary hardware failure.
Special files, empty directory snapshots and full permission/ACL preservation
remain unsupported. Literal links require capability-aware readers/writers and
actual host support; incapable hosts refuse materialization without copying
targets or dropping historical metadata. Local metadata must be trusted. Keep
files quiescent during snapshot/checkout/restore/recovery. Later public conflicts
now trigger safe automatic abort: newer public entries stay untouched, original
names return only when vacant/owned, and alternate versions plus discoverable
receipts stay under `.pebble/recovery`. A fresh/repeated `recover` prints paths
and the aborted result, not target success. Required permission/barrier failures
remain pending until safe retry. This is not atomic multi-path/HEAD publication
against arbitrary external writers, and process-kill proof is not power-loss
qualification. Old journals may lack dirty preimages that old code never saved.

The original README comparison that Git "stores full snapshots" and therefore
necessarily bloats binaries was misleading: Git models snapshots but also uses
packfile compression and deltas. Pebble's intended benefits need comparative
benchmarks, not assumptions about Git's logical data model.
