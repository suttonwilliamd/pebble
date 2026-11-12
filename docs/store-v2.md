# Format2 local repository engine and physical packs

`internal/storev2` implements the [frozen format-2 object contract](contracts/v2.md):
typed durable object/ref publication, immutable paged files, bounded streaming
ingestion and compressed checksummed physical packs with rebuildable indexes.
`internal/repository` now powers CLI `commit`, `status`, `log`, `branch`,
`checkout`, `restore`, `recover` and `fsck`. Text/PNG/JPEG/gzip/ZIP and arbitrary bytes use the
same `IngestFile`/`CopyFile` path, without filename or binary classification.
The `/v1/` service remains legacy-only. There is no implicit migration, format2
sync, merge command, GC or Git importer.

## PB-09: recoverable checkout and restore

Checkout and restore now publish an immutable, versioned, SHA-256-checksummed
local intent in `.pebble/worktree-journal` before any working-path mutation.
This is repository coordination above the immutable store, not another object
codec or format-2 change. No staging index is introduced: the coherent index view
remains the committed HEAD tree.
The encoded intent is bounded to 64 MiB and the existing graph-count limit;
oversized plans fail before any working-path mutation.

Intent pins source/target symbolic HEAD and commit IDs, the exact affected path
set, original/target file identities, sizes, raw hashes, executable modes and
modification times, and existing/new directory identities. Verified target files
are synced and closed in a private `worktree-stage-*` directory; publishing the
whole stage reuses migration's file/directory barriers and no-replace publication.
Original files are synced before intent, then atomically moved into retained
`old-*` slots; verified targets move out of `new-*` slots without replacement.
Directory creation is likewise a recorded, no-replace directory move. These
identities and locations encode progress without a mutable last-step counter.

`Repository.CheckoutContext`, `RestoreContext` and `Recover(context.Context)`
support cancellation. Existing Checkout/Restore signatures remain. Reopen is
read-only; `pebble recover` explicitly resumes the pending original intent.
All recovery material, branch pins and affected live paths are checked before
resuming. Recovery never infers ownership from matching bytes alone. Unexpected
edits/deletions/replacements, aliases, non-files, directory substitution, missing
material, invalid checksums/versions/shape and branch changes return errors,
retaining the journal. Resolve conflicts only after preserving both the complete
directory and retained material; there is no force/abort command.

Recovery rolls forward, not back: it completes the original branch checkout or
the explicitly requested discard-one-tracked-file restore. It can be repeated.
Before HEAD CAS, completed namespaces are re-barriered (POSIX directory sync;
Windows identity-preserving moves through recorded target slots). HEAD retries
re-publish through store validation/CAS/barriers, including when already equal.
Only after files and HEAD are coherent and durable is the whole active journal
retired by a durable directory rename to `worktree-completed-*`; cleanup cannot
leave a partially deleted active intent. A cleanup interruption may leave private
completed debris; a pre-intent interruption may leave an unused `worktree-stage-*`.
Neither is a pending transaction. Do not treat a publication/barrier error as
success merely because the new bytes are visible.

Persistent `worktree.guard` and `write.guard` use nonblocking OS process locks
(flock / LockFileEx), released by the OS on process death. Never unlink a guard.
Existing legacy exclusive lock markers remain fail-closed; no PID guessing or
automatic legacy-lock removal occurs. Do not run older binaries concurrently.
Pending intent blocks commit/branch/checkout/restore/status; object inspection,
log and fsck remain read-only and do not claim that a partial tree is clean.

Checkout still refuses tracked dirty work and conflicting untracked/ignored
paths, and adopts only identical, exactly spelled untracked files without
rewriting them. Restore still explicitly discards only its selected tracked
file's edits, retaining that original until completion. Unrelated files are
untouched. Existing case/normalization/path and corruption guards remain.
File/directory transitions remain rejected (PB-10); no new ignore behavior.
Editors and other noncooperating namespace writers must remain quiescent. A race
detected after moving an original retains its exact bytes in the journal, not
necessarily at the original pathname. These guards are not an adversarial
filesystem sandbox or hardware/controller power-loss certification.

Regression coverage uses generated disposable trees: every observed transaction
fault/child-exit boundary, explicit process kill/reopen and built-CLI recovery,
idempotent retries, cancellation, live cross-process exclusion, dirty restore
originals, post-interruption external edits/identical replacements, directory and
case aliases, and corrupt/incomplete/unsupported journals. Existing executable
tests cover packed mixed binary/compressed/repeated-path histories and preflight
dirty/untracked/corrupt-source protection. Validation results are reported per
toolchain/platform separately from remote CI; process exits do not cut power.

## PB-08: explicit verified v1 migration

`repository.MigrateV1(ctx, exactSourceRoot, freshDestination, MigrationOptions)`
and `pebble migrate-v1 <source> <destination> [--branch <name>]... [--head <name>]`
provide an explicit offline copy, never an in-place upgrade or legacy fallback.
Empty selection means all branches; HEAD defaults to source HEAD and must be
selected. Excluding that branch requires an explicit `Head`/`--head`.
Source discovery/constructors are not called. Existing locks cause failure, not
cleanup. Every source file is opened read-only; no repair or metadata creation.

The loader validates shipped snapshot JSON envelopes, raw lowercase SHA-256
addresses, kinds/sizes, flat trees, symbolic refs and each optional parent.
The embedded pre-self-hash is validated when present and intentionally omitted
from v2 commits; frozen fixtures with an empty embedded hash remain supported.
Canonical JSON from the shipped structs is required (outer whitespace allowed).
Unknown/duplicate fields, alternate schemas, invalid UTF-8 spellings and
unrepresentable identities/dates/messages/modes/ref namespaces fail explicitly.
Modes must be `-rw-r--r--` or `-rwxr-xr-x`, not arbitrary permission bits, including
`-rw-rw-rw-`. No identity, branch name, parent, path spelling or bytes are invented.

All selected ancestors are converted parent-first using `IngestFile` (and its
`CreateManifest` path), `CreateTree`, `CreateCommit`, ref CAS and HEAD CAS.
Every historical tree passes repository path/collision guards and actual filesystem
namespace probes. Source IDs map to typed manifests/trees/commits, not relabeled
v1 hashes. Reopened v2 reads compare every commit's identity/date/message/parent
mapping, every tree/path/mode/manifest mapping, and exact `CopyFile` bytes plus
independent whole-file SHA-256 and lengths for every historical blob. Ref catalog
and selected HEAD are checked independently. HEAD files are restored in the
private stage with existing repository guards and staged streaming writes.
A working executable-mode mismatch fails rather than clearing the stored bit.

The whole conversion and `.pebble/migration-v1.json` receipt stay inside a private
`.pebble-migrate-*` sibling until publication. Overlap, aliases, symlink/junction
components and existing destinations are refused; real destination parents must
exist. Every consumed source file and the full HEAD/ref catalog are re-read before
publication to detect changes. The caller must keep the source quiescent; this is
detection, not a source write lock. Working files/index/settings/credentials are
not migration input or copied history. A single unborn `heads/main` is supported;
other selected unborn layouts cannot be faithfully represented in current v2.

Local policy bounds whole legacy JSON envelopes to 128 MiB and legacy commit/tree
payloads to 1 MiB, plus v2 constructor/graph bounds. Oversize/deep/incomplete input
fails without shortened history or a successful result. Legacy decoding retains
one bounded envelope/file; this is not a large-v1-file memory qualification.

Publication syncs staged files and installs the entire directory without replacing
an existing name: fixed-local-NTFS write-through `MoveFileEx` on Windows,
`renameat2(RENAME_NOREPLACE)` on Linux, `renameatx_np(RENAME_EXCL)` on macOS,
and POSIX directory barriers. Unsupported primitives fail without copy/delete.
Ordinary errors remove only the private stage; abrupt termination may leave it.
A namespace/barrier error is not success and may leave a complete destination after
uncertain publication. Preserve/inspect/fsck it; never retry into or automatically
delete it. Physical power-loss qualification is not implied.

Focused disposable-fixture checks:

```sh
go test ./internal/repository -run '^TestMigration' -count=1
go test ./cmd/pebble -run '^TestExecutableMigration$' -count=1
```

The regression builds and launches the actual CLI, not mocked forwarding.
Cases cover selected/shared history, repeated-path PNG/JPEG/gzip/ZIP/noise, empty
blobs/trees/history, exact source-state preservation, corrupt/missing dependencies,
unsupported metadata/modes, unsafe paths/overlap/aliases/existing destinations,
cancellation, competing publication, the actual exclusive rename and process
termination before publication. No personal repository migration, scale benchmark,
or physical-crash qualification is claimed.


## PB-06: local CLI cutover

`pebble init` uses `InitRepository` to publish only new metadata in an existing
working directory. Existing files are preserved; existing/partial metadata and
initialization remnants are refused. The stricter library `Init` still requires
an empty directory. Discovery stops at the nearest `.pebble`; legacy/unmarked,
unknown and incomplete repositories fail explicitly, never fall through to an
ancestor or another engine. Legacy push/fetch and service code reject markers.

Canonical tree/commit helpers reuse the store decoder, envelope validation and
publication. Commit parents retain their supplied order; signed seconds,
nanoseconds, exact offset spelling, identity and message survive read/write.
Log follows all parents in supplied order, first-parent first, without duplicates.
Status streams raw SHA-256 and executable mode comparisons against HEAD, not an
index or a filename heuristic; ignored tracked files remain tracked.

The repository engine holds an OS lock on `.pebble/worktree.guard` around cooperating
commit/branch/checkout/restore/recovery/status operations. Branch refs are published only through
`UpdateRef` graph validation, fast-forward/CAS and durability. HEAD is separate
symbolic metadata with exact CAS and verified targets; only the initial unborn
`heads/main` may lack a commit. Additional branches require the first commit.

Checkout preflights dirty tracked files, unsafe/case-aliased paths, non-files and
untracked/ignored collisions. It probes target names and directory components in
private staging using the actual filesystem's alias rules before any working-file
mutation. Existing components must match their enumerated spelling exactly:
normalization aliases cannot be adopted or traversed through an untracked path.
Unrepresentable trees are refused, never Unicode-normalized into different paths.
Filesystems that distinguish canonically equivalent spellings retain both.
Identical untracked files with exact path spellings may be adopted. All target
files are staged through verified `CopyFile`, synced and closed before intent
publication. Restore verifies one tracked HEAD file before discarding its edits.
Provisional bytes are never installed after a copy failure. File/directory
transitions remain explicit errors. PB-09 replaces ordinary best-effort rollback
with the durable recovery journal described above.

`fsck` uses the public `Inventory` facade to validate every published loose and
packed physical copy, including unreachable objects and duplicate representations.
It then calls `Verify` for every unique typed ID and verifies symbolic HEAD and
all ref closures. A physically valid but incomplete/unreconstructable unreachable
graph is an error, not a skipped object. Derived pack indexes remain rebuildable;
fsck never scans private backend paths or repairs metadata. Inventory is not a
transactionally frozen snapshot of concurrent additions.

The actual executable regression builds the CLI and launches a fresh process per
operation on disposable mixed fixtures and repeated-path edit/insertion/truncation/
empty histories. It compares exact restored bytes and independent SHA-256 for
every revision and encoded asset, and exercises dirty/untracked/ignore guards.
It packs the mixed history through public `Pack`, then reopens the actual CLI for
log/branch/status/checkout/restore/fsck and a new commit/ref publication. Loose
copies remain intact; packed copies must validate too. An unreachable incomplete
tree must fail actual CLI fsck without changing valid working files.
An isolated packed orphan with a canonical envelope but false raw checksum must
also fail fsck while its valid branch and exact user bytes remain intact.
Additional regressions cover canonical golden metadata, ordered parents, legacy
source preservation, nested incomplete discovery and late-checksum staging failure.
Normalization regressions cover aliased absent targets, directory components and
existing untracked aliases with byte/HEAD preservation, explicitly skipping where
the fixture filesystem distinguishes the spellings. A complementary regression
verifies independent bytes and clean status for distinct spellings on those
filesystems. macOS CI runs these checks verbosely to expose exercised/skipped cases.
Run `go test ./cmd/pebble ./internal/repository ./internal/storev2 -count=1`.
No large-binary, arbitrary power-loss or format2 remote qualification is implied.

## API and validation boundary

All I/O operations take `context.Context` except the read-only exact-root opener.
Cancellation is checked between reads and immediately before publication; it
cannot interrupt an arbitrary blocking caller-supplied reader or kernel flush.

| API | Behavior |
| --- | --- |
| `Init(ctx, emptyDirectory)` | Explicit initialization of an existing **empty** directory only. Publishes a complete `.pebble` directory and exact format marker; never resets/upgrades existing data. |
| `Open(root)` | Opens exactly that root; no ancestor discovery, creation or migration. Requires exact marker, ordinary metadata directories and supported filesystem primitives. |
| `ParseID(text)` | Strict `pv2:<kind>:sha256:<lowercase full digest>`, kinds 01..06 only. |
| `Header(kind, payloadLength)` | Fixed 13-byte canonical header; rejects unsupported kinds and oversize payloads before allocating payload storage. |
| `NewID(kind, payload)` | Validates bounded payload schema and hashes the complete header+payload without constructing a second envelope copy. Does not resolve dependencies. |
| `Put(ctx, expectedID, canonicalEnvelopeReader)` | Verifies kind, exact length/EOF, schema and typed address while staging bytes, then durably publishes one object. Incomplete dependencies are allowed during transfer. |
| `Copy(ctx, id, destinationWriter)` | Streams a canonical envelope, checking schema/address. Output is provisional until nil error: discard it on any failure. |
| `Verify(ctx, rootID)` | Verifies complete closure, every object address/type, every parent/tag target, page partitioning, chunk/page/file lengths and reconstructed raw whole-file SHA-256. |
| `CreateManifest(ctx, nextChunkRef)` | Consumes ordered references to already-stored chunks, checks their actual bytes/lengths, derives the raw file SHA-256, and publishes canonical immutable pages and root. No pathname/ref is changed. |
| `IngestFile(ctx, rawReader, policy)` | Streams arbitrary bytes through ROCK content-defined boundaries into typed chunks and `CreateManifest`. Nil policy selects ROCK defaults; returns one immutable manifest ID, never a pathname/ref update. |
| `CopyFile(ctx, manifestID, destinationWriter)` | Streams raw file bytes, validating all typed addresses, page partitioning, exact lengths and whole-file checksum. Output is provisional until nil error; discard on failure. Nil destination verifies without retaining output. |
| `UpdateRef(ctx, name, expectedIDPointer, targetID)` | Nil expected = absent; exact CAS + fast-forward branches, create-only tags, graph verification, collision checks and atomic durable publication under the writer lock. Identical retries also reverify and establish a fresh durability barrier. |
| `GetRef(ctx, name)` | Returns a target only after validating ref metadata and complete graph, including annotated tag name binding. Missing refs wrap the existing `REF_NOT_FOUND` category and `os.ErrNotExist`. |
| `InitRepository(ctx, workingDirectory)` | Creates only new format2 metadata and symbolic unborn main; preserves working files and rejects existing/partial metadata. |
| `CreateTree` / `ReadTree` | Canonical typed tree bindings; sorted input is required, never silently reordered. |
| `CreateCommit` / `ReadCommit` | Canonical metadata with ordered distinct parents and exact signed/fractional time and offset spelling. |
| `FileInfo(ctx, manifestID)` | Verified manifest metadata length/raw SHA-256; not a replacement for `CopyFile` reconstruction checks. |
| `ListRefs(ctx)` / `VerifyRefs(ctx)` | Verified catalog and shared bounded closure walk, including tags; returns owned targets / reachable-object count. No private-layout walking in the repository engine. |
| `Head(ctx)` / `UpdateHead(ctx, expected, target)` | Bounded canonical symbolic HEAD; writer-locked CAS, graph-verified target and durable metadata publication, never a branch-ref write. |
| `Inventory(ctx)` | Returns sorted unique typed IDs after validating every published loose and packed physical copy, including unreachable objects. Read-only; callers can `Verify` each ID for closure checks. |
| `Pack(ctx, ids)` | Explicitly writes a deterministic immutable pack of selected objects, with compression only when smaller. Preserves all loose objects, IDs, manifests and refs. Returns the physical pack digest, not a logical object ID. |
| `RebuildPackIndexes(ctx)` | Under the existing writer lock, validates every complete pack before publishing any rebuilt index/catalog; recovers interrupted publication without deleting data. |

A structurally valid object may be stored before its dependencies arrive. This is
not a claim that its graph/file is valid: only `Verify` or successful `UpdateRef`
establishes closure and semantic integrity. No ref is published on a missing,
mis-typed, corrupted or semantically invalid dependency. A corrupt preexisting
object at an expected address is **never overwritten**, including by a retry
providing otherwise correct replacement bytes. An existing correct object is
verified and identically republished to establish a fresh namespace barrier;
this is not zero-I/O deduplication or a storage-performance claim.

Before acknowledging a ref, a cold/reopened handle also revalidates and
identically republishes closure objects whose completed barriers it has not
observed. Visible valid bytes alone do not prove namespace durability after a
prior uncertain write. A bounded, mutex-protected per-handle cache remembers at
most 100,000 successful object publications to avoid redundant copies on the
normal same-handle path. It never skips address/content/graph validation, is not
persisted as a substitute for a barrier, and is empty on `Open`. Cold first-write
cost is intentionally higher; this is a correctness boundary, not a benchmark
optimization claim.

Errors retain object/ref/operation context and wrap existing Pebble error codes.
`errors.Is` also recognizes `ErrInvalidObject`, `ErrFormat`, `ErrLocked`,
`ErrConflict`, `ErrGraphLimit`, `ErrPublicationUncertain` and underlying I/O or
context errors. Negotiation/ref-update messages (80..82) are never stored objects;
PB-03 does not implement protocol negotiation itself.

## Private on-disk layout

```
root/.pebble/
  format                    exact bytes: pebble-format 2\n
  objects/KK-DIGEST.obj      complete canonical envelope, KK = 01..06
  refs/NAMEHASH.ref          complete ref metadata
  pack-PACKHASH.pack        optional immutable physical container
  pack-PACKHASH.idx         checksummed derived index
  pack-catalog              checksummed derived pack inventory
  write.guard               persistent OS writer-lock guard (never unlink)
  worktree.guard            persistent OS working-tree guard (never unlink)
  worktree-journal/         optional active checkout/restore recovery intent
```

`DIGEST` is 64 lowercase hex digits. `NAMEHASH` is lowercase SHA-256 of the exact
UTF-8 ref name, so path separators and case-insensitive filesystems cannot alias
physical ref files. A ref file is `u32 name_length || ASCII name || kind:u8 ||
digest:32`; its filename must agree with its embedded name. This local metadata
layout is not an addressable object or a protocol message. Ref names are bounded
to 4,096 bytes in this engine. Ref names/targets, branch kind and annotated tag
name binding are checked; case-insensitive equality and ancestor/descendant
collisions are rejected within each namespace. The catalog is scanned in bounded
batches, and malformed/unknown published entries fail closed. Only ordinary
`.pebble-tmp-*` files are ignored as unpublished interrupted writes.

Objects and refs use flat directories: no dynamically created shard/branch
directories need additional barriers during publication. All published files
must be regular files; symlinks, directories or other special files at an object,
ref or marker path are rejected. Metadata directories must be ordinary
directories. The repository and its filesystem ancestry remain locally trusted;
this is not a hostile-filesystem sandbox or protection against unrelated tools
modifying files/mounts concurrently. Locks serialize cooperating writers across
processes, not editors, old binaries or external mutation.

## Publication and durability

1. Check context, exact format marker and directory guards; acquire
   `.pebble/write.guard` with an OS process lock; repeat the format guard under lock.
2. Validate any preexisting target object or current ref/graph before replacing
   it. For refs, validate the new complete graph, CAS, ancestry, names and types.
   Reestablish unknown closure-object durability through the same real file and
   namespace barriers before publishing the ref; a failed closure barrier
   cannot be replaced by successful graph validation.
3. Write a same-directory temporary regular file. Input is bounded before
   allocation; chunks stream through reusable 32 KiB buffers. Metadata payloads
   are bounded to 1 MiB, chunks to 8 MiB, and exact EOF is required.
4. Sync and close the staged file. Recheck context before publication.
5. Atomically replace the destination and apply the platform namespace barrier.
   Do not acknowledge success if any operation/barrier fails.
6. Release the writer lock. An unlock failure is reported, not ignored.

POSIX backend: file `Sync`, same-filesystem `rename`, then parent-directory
`Sync`; initialization also syncs created directories before publishing them and
their parent. A filesystem that rejects directory sync fails closed.

Windows backend: fixed **local NTFS only**, file `Sync`, then
[`MoveFileExW`](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw)
with `MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH` for files, or
`MOVEFILE_WRITE_THROUGH` for created directories. No `MOVEFILE_COPY_ALLOWED`,
copy/delete fallback, ignored directory-sync error or successful no-op durability
shim. Removable/network/non-NTFS volumes are rejected. This backend relies on the
OS's documented write-through rename contract; filesystem, storage-driver and
hardware behavior still require independent crash testing. Nothing here proves
physical persistence under arbitrary power/controller failure.

A failure before rename leaves the old value (or absence). A namespace-barrier
failure or lost acknowledgement **may leave the complete new value visible**;
`ErrPublicationUncertain` reports this rather than rolling back blindly or
claiming that nothing happened. There is never an acknowledged partial ref.
Reopen/verify and retry the **same** intent/bytes. The retry revalidates content
and republishes through the durability barrier; it cannot hide corruption.

An interrupted writer releases its OS lock and may leave unpublished temps;
the persistent guard file itself is not evidence of a live owner. Legacy
`write.lock`/`worktree.lock` markers still fail closed and require inspection, never
automatic removal. Initializers additionally use `.pebble-init.lock` in the empty
root and an unpublished staging directory. Failed/incomplete initialization is
not repaired or reclassified as a new repository; inspect it manually. Do not
convert a v1 directory by adding the marker.

## PB-07: physical packs and recovery

Pack metadata is optional in format 2. `Init`, `Open` and ordinary loose-object
reads do not create it; `Pack` and `RebuildPackIndexes` explicitly publish flat
`pack-*` files in the existing `.pebble` directory. No new-directory barrier is
needed, including after an uncertain Windows publication result.
Older loose-only repositories remain readable without a marker or ID change.
There is no removal/reclamation API. Only disposable tests remove loose copies
to establish that reopened reads genuinely use packs.

All physical integers are big-endian. Pack version 1 is:

```
"PBPACK\x00\x01" || count:u32 ||
  repeated(kind:u8 || logical_digest:32 || codec:u8 ||
           canonical_envelope_length:u64 || stored_length:u64 || stored_bytes) ||
SHA256(all preceding pack bytes):32
```

Records are strictly ordered by kind and logical digest; duplicates are errors.
Codec 0 stores the canonical envelope verbatim; codec 1 stores one zlib stream
containing that same envelope. Compression is selected only when smaller.
`PACKHASH` is the final checksum in lowercase hex. It addresses the physical
container, not its logical objects. Typed IDs still hash the exact original
`PB2` envelope; refs, ordered manifest links and whole-file checksums are unchanged.
Reconstructed envelopes pass the existing typed schema, length, EOF and address
validator. Decompression is bounded to the declared raw length plus one byte;
zlib checksum failures, extra streams/trailing bytes and declared length mismatches
are errors, not successful short files.

An index is `"PBINDX\x00\x01" || count:u32 || PACKHASH:32 || entries ||
SHA256(prefix):32`. Each 58-byte entry is the record's typed ID (33 bytes),
codec (1), raw length (8), stored length (8), and absolute data offset (8).
Reads check the whole pack checksum and framing and compare the bounded index
with the exact derived entries before trusting it. A missing or corrupt ordinary
index triggers complete canonical pack validation and in-memory reconstruction,
never an implicit disk write. Special files and filesystem access/I/O errors
are not disposable index corruption. This conservative implementation scans
compressed pack bytes per lookup; it does not claim constant-time indexed I/O
or cache validation across external file modifications.

The catalog is `"PBCATL\x00\x01" || count:u32 || sorted PACKHASH:32 entries ||
SHA256(prefix):32`. Complete `.pack` files are the recovery source of truth.
A missing catalog, or a valid older catalog omitting a newly published pack, is
recoverable. A catalog claiming a missing pack, invalid checksum/version, orphan
index, unexpected published `pack-*` filename or special file fails closed on reads.
Explicit rebuild can replace damaged ordinary derived catalog/index bytes.
Unpublished ordinary `.pebble-tmp-*` files are ignored, never reclaimed here.

Publication under the existing exclusive writer lock:

1. Validate selected canonical objects and all existing packs; stage a bounded
   complete pack and checksum.
2. Sync/close/publish the pack through `finish` and the existing actual platform
   namespace barrier. No index can reference a partially written pack.
3. Validate **all** complete packs and every logical record before publishing any
   derived index. Reaffirm the pack file/namespace barrier, publish its index,
   then publish the catalog last. Every file uses the same sync/close/atomic
   publication/error boundaries as loose objects and refs.
4. Only after all barriers succeed, memoize packed object durability. Every ref
   publication repeats real pack, index and catalog barriers before publishing
   the ref, including when loose copies remain or this handle published earlier.
   This conservative physical barrier is not skipped by the loose-object cache
   and does not manufacture loose fallback copies.

Interruption may leave a complete pack without its index/catalog. Read-only reopen
can reconstruct its index in memory; explicit rebuild or the identical `Pack`
retry verifies and republishes the durable metadata. A failed barrier is propagated,
including `ErrPublicationUncertain` when publication may have happened. Existing
refs remain unchanged on failed closure barriers. Never steal a stale lock:
confirm process exit, inspect/verify, then make an explicit recovery decision.
Rebuild validates all packs first, so corruption cannot install a false index
for an earlier valid pack. It does not repair corrupt primary pack bytes.

Local physical policy: at most 1 GiB per pack, 1,024 packs, 100,000 aggregate
physical packed records, canonical per-kind raw limits, stored records bounded
by raw length plus 64 KiB, and bounded directory/index/catalog input. Raw records
stream from bounded file sections; zlib uses a 32 KiB byte-reader whose read-ahead
remains visible to trailing-byte checks. Packing reuses one pair of bounded
raw/compressed object buffers and one compressor, not a whole-pack buffer.
Duplicate copies across packs are allowed and counted toward the physical limit;
duplicates within a pack are rejected. `Inventory` validates every copy instead
of hiding a corrupt packed duplicate behind a good loose object.

The pack tests use the real PB-02 noisy/compressed PNG/JPEG/gzip/ZIP corpus, raw
and zlib records, byte-exact pack-only reopen, frozen graph/ref fixtures, missing
and checksum/offset-corrupt indexes, adversarial checksummed records, injected
actual publication boundaries and child-process exits after pack/index/catalog
publication. Child exits exercise process interruption, **not** power failure.
The existing Windows local-NTFS/POSIX durability contracts above still apply;
hardware/controller persistence and independent OS crash qualification remain
unproven by these tests.

## Resource policy

In addition to frozen per-record bounds, complete verification is iterative and
fails closed above 100,000 objects, 1,000,000 edges, 64 MiB accounted metadata,
4,096 dependency depth, or 1 TiB aggregate inspected bytes (including rereads for
file checksums). Ref catalog scans are capped at 100,000 entries. These are
explicit local resource limits, not changes to canonical IDs or wire bytes.
Graph verification retains bounded metadata, not reconstructed files or all
chunk payloads; checksums are computed from actual ordered chunk bytes. Repeated
chunks/pages remain ordered repetitions, not duplicate-map errors. Fast-forward
checks follow all commit parents, not only first-parent history.

## PB-04: constructing and reading file revisions

`ChunkRef{ID, Length}` names one already-stored, nonempty chunk and its exact raw
byte length. `CreateManifest` calls `next func() (ChunkRef, error)` synchronously
until `io.EOF`. Any other error aborts, even if returned alongside a reference.
An EOF terminates the sequence without consuming that call's reference. An empty
sequence creates the canonical empty root: zero length, raw SHA-256 of empty
bytes, no pages. Chunk ingestion and boundary selection remain PB-05's job;
there is no fixed-size or CDC policy in this API.

The constructor reads actual chunk bytes through the store's existing verified
streaming loader, not caller-provided checksums. It packs exactly 1,024 entries
per nonfinal page and 1..1,024 per final page, with at most 16,384 root pages.
Lengths are checked before accumulation. Raw whole-file SHA-256 follows the
ordered chunk occurrences, including repeated chunks and repeated full pages.
Pages/root use the existing envelope codecs, typed addresses, guarded `Put`
and durable object publication; there is no second schema or storage layout.

Construction retains only the bounded root (maximum payload 671,788 bytes), one
page (maximum payload 41,988 bytes), and a reusable 32 KiB I/O buffer, plus the
store's bounded per-object validation buffers/metadata. `CopyFile` retains one
decoded root and one page at a time and streams chunks through the same reusable
buffer. It shares manifest semantic verification with `Verify`, which reuses its
already-validated page metadata. Standalone file operations do not accumulate a
graph-wide object map; their bounds are the canonical manifest counts. Complete
graph/ref verification still applies the stricter local resource policy above.

Each returned manifest ID identifies one immutable file revision, not a pathname.
Later roots may share unchanged pages/chunks without modifying historical roots.
Existing corrupt objects are never repaired or overwritten by construction.
On error, no successful revision ID is returned, but unreferenced completed pages
may remain; an uncertain final publication may also leave a complete root.
Retry the same sequence. No tree, branch or tag is implicitly published.

`CopyFile` requires a manifest-kind ID and verifies every page/chunk address,
kind, framing, declared length, canonical partition and final raw checksum.
Missing dependencies are errors, never empty files. Streaming output may have
been delivered before a late error: callers must stage/discard their destination
as appropriate. There is no destination crash-atomicity or checkout journaling.
Context cancellation is checked between reads and publications, but cannot
interrupt an arbitrary blocking iterator, reader, writer or kernel flush.

PB-04 regression coverage uses the same frozen PB-01 goldens, including empty,
1,024/1,025-entry boundaries and repeated full pages. The shared PB-02 mixed
corpus includes real PNG/JPEG/gzip/ZIP encodings and repeated-path edits,
insertions, truncation and empty revisions. Tests reconstruct each historical
tree binding independently after reopening, compare exact raw bytes and
independently computed length/SHA-256, verify unchanged-page sharing, reject
missing/corrupt/short/trailing/out-of-order content and invalid lengths/checksums,
and exercise iterator errors, writer errors and cancellation. A repeated
maximum-size chunk case streams 16 MiB to a byte-checking bounded sink. This is
not large-binary qualification, full 16,384-page capacity traversal, power-loss
evidence or CLI availability.

## PB-05: bounded content-defined ingestion

`IngestFile(ctx, source, policy)` composes ROCK's pull-based `ChunkReader`,
existing typed chunk publication, `ChunkRef` and `CreateManifest`. Pass nil for
the default policy, or a `*rock.FastCDC` configuration. Invalid sizes and maximum
chunks above the canonical 8 MiB bound are rejected before buffer allocation.
The stream snapshots the configuration; later caller changes cannot alter its
boundaries. Input bytes are never filtered by filename, extension, text/binary
classification or compression. PNG/JPEG/gzip/ZIP and incompressible bytes use the
same path, without decompression, normalization or a new storage encoding.

### Boundary policy and buffer ownership

This preserves the repository's existing ROCK `FastCDC` policy, not a new format
or a claim of interoperability with another FastCDC implementation:

- Defaults: minimum 512 bytes, maximum 8,192 bytes, rolling window 48 bytes,
  polynomial prime 2,083,806,171, mask `0x3fff`, target `0x1000`.
- Starting at the window size, update the 32-bit rolling hash with each candidate
  byte. At positions at least the minimum, a matching masked hash cuts **before**
  that candidate byte.
- At positions at least `min + (max-min)/2`, also look ahead up to 64 positions
  (before the maximum) for a zero followed by nonzero; cut immediately after the
  zero. If neither rule matches, cut at the maximum or the remaining EOF bytes.
- The final chunk may be shorter than the minimum. No empty chunk is emitted.

`NewChunkReader` fills one `max+1` byte buffer before deciding a boundary, unless
EOF arrives first. The extra byte supplies the zero/nonzero lookahead at the
maximum. Consequently short reads, one-byte reads and data-plus-EOF cannot change
boundaries for identical bytes/configuration. Batch, callback and pull APIs use
one shared boundary implementation. `Next()` returns a borrowed slice valid only
until the next `Next()` call. Ingestion finishes publication before advancing;
there is no chunk copy, chunk/reference history, producer goroutine, channel or
filename-keyed index bridging the APIs. The older `ChunkStream` callback still
receives owned chunks that callers may retain.

Default chunk-buffer capacity is 8,193 bytes; maximum configured capacity is
8 MiB + 1. Manifest state remains one page and a bounded root as above, plus
32 KiB reusable store I/O buffers, bounded metadata parsing and the existing
100,000-entry durability cache. Live memory is bounded by these explicit limits,
not input length. Temporary allocations and total allocation work still grow
with chunk count. Root growth/parsing can transiently hold additional bounded
metadata copies; this is not a zero-allocation or fixed-RSS claim.

### Publication, deduplication and failures

`CreateManifest` holds no outer writer lock; each typed `Put` independently
acquires/releases the existing writer lock. Do not wrap ingestion in another
writer lock or call it from a callback that already holds one. Concurrent writers
can fail with `ErrLocked`; there is no retry policy here.

Identical chunk bytes share one typed address/object across files and revisions.
Identical sequences/configurations share the manifest ID; edits may reuse chunks
and full unchanged pages. Existing roots/tree bindings are never mutated. As
with `Put`, a duplicate is verified and identically republished for durability:
deduplication avoids duplicate stored content, not all duplicate I/O. This API
does not use the legacy ROCK pathname-index database.

Any non-EOF read error, including explicit `io.ErrUnexpectedEOF` or an error
accompanying the last/full-buffer bytes, aborts ingestion. A reader cannot hide
that failure by returning only EOF afterward. Valid last bytes plus `io.EOF`
are consumed normally. A persistently non-progressing reader fails with
`io.ErrNoProgress`. Cancellation is checked between reads/publications; blocking
caller I/O cannot be forcibly interrupted. Callback errors in `ChunkStream`,
publication errors and `CopyFile` writer/short-write failures propagate.

On failure there is no successful manifest ID or implicit tree/ref update.
Already-published unreferenced chunks/pages can remain, and an uncertain final
publication can leave a complete root. Discard the zero ID/error result and retry
the same intent; do not treat a visible object as acknowledgement. Corrupt shared
objects fail closed and are not silently repaired. Restore output is provisional
until `CopyFile` returns nil.

### Reproducible generated-input evidence

```
go test ./internal/storev2 -run '^TestIngestGeneratedStream$' -count=1 -v
```

This deterministic scenario generates SHA-256 counter blocks incrementally,
ingests with minimum 8 KiB/maximum 32 KiB/window 48, reopens the actual store and
compares every restored byte with a separate bounded generator. Neither complete
input nor reconstructed output is retained. The 32 MiB case crosses two immutable
manifest pages; the test checks exact length/checksum and bounded I/O requests.

Observed on Windows/amd64 Go 1.21.13, `GOTOOLCHAIN=local`, offline module mode,
CGO disabled:

| Input | Pages | Largest read request | Largest restore write | Sampled Go heap baseline / peak | Total allocated during ingestion |
| --- | ---: | ---: | ---: | ---: | ---: |
| 1 MiB | 1 | 32,769 B | 21,342 B | 370,448 / 1,593,640 B | 1,223,192 B |
| 32 MiB | 2 | 32,769 B | 21,924 B | 441,776 / 3,875,984 B | 31,908,056 B |

The run passed in 35.933s (1 MiB subcase 1.06s; 32 MiB subcase 34.40s).
Heap is sampled every 32 source reads and after ingestion with `runtime.MemStats`;
it is **not RSS, an exact peak, or an asserted performance target**. These values
include runtime/store effects and will vary. The architecture and checked buffer
bounds, not two samples alone, establish the bounded streaming policy.
This does not qualify PB-16's 10 GiB/256 MiB target, CLI ingestion, power-loss
behavior, large-binary throughput, or Linux/macOS runtime performance.

Permanent preservation tests also ingest the PB-02 real mixed/compressed corpus,
restore every repeated-path revision independently after reopen, check cross-file
and edited/inserted-revision sharing, and exercise fragmentation, canonical
maximum chunk size, final-bytes errors, cancellation, publication faults and
corrupt deduplication candidates. PB-04's existing traversal tests retain
missing/short/trailing/out-of-order/checksum and destination-error coverage.



## Exercised evidence and limits

From the repository root, using cached Go 1.21.13 `windows/amd64`, CGO disabled,
`GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`:

```
go test ./internal/storev2 ./internal/formatcontract -count=1
```

Initial run: store 6.338s, frozen contract 0.204s, both passed. The production
suite reads the **same** 185 PB-01 wire records: stored-object conformance and
closure, marker acceptance/rejection, and rejection of non-addressable protocol
records. Additional cases cover equal payloads across distinct types,
preexisting corruption (type/size/content/truncation/trailing bytes), corrupt ref
metadata, missing closure, stale/non-fast-forward CAS, second-parent ancestry,
tag target/name/immutability rules, ref collisions, changed format guards,
maximum-size chunk streaming, shared PB-02 fail-after readers, cancellation,
publication-boundary fault injection and retry.

Separate child processes prove exclusive writer locking and interruption before
and after object/ref publication. They deliberately exit without deferred cleanup,
then prove complete old/new state, retained stale locks and explicit recovery only
after the child exited. This is **process-interruption evidence**, not power-loss
proof. Injected file-sync/close/namespace/acknowledgement errors are boundary
fault-injection evidence, not real hardware failures. Symlink cases run only
where the OS grants test symlink creation. POSIX execution, additional platforms,
race detection and full integration checks belong to the feature PR's CI; do not
substitute successful cross-compilation for runtime durability evidence.

After adding the cold-closure publication barrier, the full production package
check passed again in 5.719s. Final codec/allocation changes were exercised by
`go test ./internal/storev2 -run '^(TestProductionGoldenConformance|TestStreamingFaultsCancellationAndLimits)$' -count=1`
(4.026s), and `go vet ./internal/storev2` passed under the same offline Go 1.21.13
Windows environment. These timings are observations, not performance targets.

Native-review follow-up: exact header/metadata reads preserve non-EOF source
errors even when the final successful bytes accompany that error and subsequent
source reads return only EOF. `io.ReadFull` success alone is insufficient here.
`TestTerminalSourceErrorsNeverPublish` covers the empty-chunk header, tree
metadata and streaming-chunk boundaries with non-sticky final-bytes-plus-error
sources, healthy retries and valid final-bytes-plus-EOF sources. The focused
terminal-error, existing streaming-fault and production golden tests passed in
4.257s on the same offline Go 1.21.13 Windows environment; scoped vet passed.

### PB-04 local verification

On Windows/amd64, both installed Go 1.21.13 and Go 1.27.3 passed the existing
CI commands below with `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off` and
`CGO_ENABLED=0`:

```
go mod verify
go build ./...
go vet ./...
go test ./... -count=1
```

Store package observations in those full runs: 22.992s on Go 1.21.13 and 23.414s
on Go 1.27.3. The preceding unfiltered minimum-toolchain focused run passed
`go test ./internal/storev2 ./internal/formatcontract -count=1` (22.096s and
0.248s). These are local correctness checks, not performance qualifications.
Linux/macOS runtime, Docker integration, race and fuzz CI were not exercised
by these Windows runs.

### PB-05 local verification

Both installed Windows/amd64 toolchains, Go 1.21.13 and Go 1.27.3, passed:

```
go mod verify
go build ./...
go vet ./...
go test ./... -count=1
```

Every command used `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off` and
`CGO_ENABLED=0`. Full storev2 observations: 84.060s (Go 1.21.13) and 80.380s
(Go 1.27.3), with the two full runs executing concurrently. These include the
generated scenario and are correctness observations, not throughput benchmarks.
The preceding focused minimum-toolchain streaming/ingestion checks passed:
ROCK 0.558s, storev2 8.916s. The separate generated-input run above supplies
the reported heap/buffer observations.

Local Windows checks did not execute Linux/macOS runtime, race detection,
fuzz campaigns or Docker integration. Those remain PR CI/platform gates;
no cross-platform, physical-crash or PB-16 large-binary result is implied.
