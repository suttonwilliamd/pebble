# Dogfood readiness evaluation

## Verdict

Suitable for **experimental local use on backed-up small projects**, not as the
sole copy of valuable work or a team Git replacement. The baseline tests passed,
but the original CLI was much less complete than its README claimed. There were
no CLI/snapshot tests covering the basic user workflow.

## Blockers addressed in this checkpoint

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

Regression tests cover local workflow round trips, complete CLI history, nested
repository discovery, ignored/previously tracked files, duplicate branches,
corruption preflight, untracked collisions, unsafe refs/paths, writer locking,
object tampering and streaming reconstruction/boundary equivalence. Symlink tests
run where OS permissions allow them.

## Next implementation priorities

1. **Recoverable checkout transactions.** Durable journal, startup recovery and
   injected failures at every filesystem step. Cover full disks, permission errors,
   cancellation, file/directory transitions, case collisions and interrupted writes.
   Current rollback is best effort, not a durability guarantee.
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
5. **Daily ergonomics.** Read-only text/binary diff, selective restore, config-backed
   identity, safe branch deletion, ignore tooling and signed release binaries.
6. **Merge and binary conflict UX.** Connect the existing combine library only
   after preservation tests and explicit resolution/recovery semantics are in place.

## Known constraints

Whole-file JSON/base64 storage and hashing still consume memory proportional to
file size. No staging, merge CLI, remotes, GC, automatic migration or durable
checkout journal. No symlinks, special files, empty directory snapshots or full
permission/ACL preservation. Local metadata must be trusted. Do not run editors
or external filesystem mutations concurrently with a snapshot/checkout.

The original README comparison that Git "stores full snapshots" and therefore
necessarily bloats binaries was misleading: Git models snapshots but also uses
packfile compression and deltas. Pebble's intended benefits need comparative
benchmarks, not assumptions about Git's logical data model.
