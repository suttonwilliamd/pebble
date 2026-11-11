# PB-03 store and PB-04 immutable manifests

`internal/storev2` implements the [frozen format-2 object contract](contracts/v2.md)
as a production library: PB-03 adds the typed durable object/ref store; PB-04 adds
immutable paged file construction and verified raw-byte reads. **The CLI and
`/v1/` service still use v1.** There is no migration, chunk-boundary policy, CDC
ingestion, checkout, tag command, new server route, pack, GC or Git importer.
Use separate disposable directories; never point an old CLI at a v2 directory.

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
| `CopyFile(ctx, manifestID, destinationWriter)` | Streams raw file bytes, validating all typed addresses, page partitioning, exact lengths and whole-file checksum. Output is provisional until nil error; discard on failure. Nil destination verifies without retaining output. |
| `UpdateRef(ctx, name, expectedIDPointer, targetID)` | Nil expected = absent; exact CAS + fast-forward branches, create-only tags, graph verification, collision checks and atomic durable publication under the writer lock. Identical retries also reverify and establish a fresh durability barrier. |
| `GetRef(ctx, name)` | Returns a target only after validating ref metadata and complete graph, including annotated tag name binding. Missing refs wrap the existing `REF_NOT_FOUND` category and `os.ErrNotExist`. |

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
  write.lock                transient exclusive writer lock
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
   `.pebble/write.lock` using create-exclusive; repeat the format guard under lock.
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

An interrupted writer leaves an exclusive lock and possibly unpublished temps.
Never automatically remove a stale lock: first establish that its writer has
exited, inspect the repository and verify relevant graphs, then make an explicit
recovery decision. Initializers additionally use `.pebble-init.lock` in the empty
root and an unpublished staging directory. Failed/incomplete initialization is
not repaired or reclassified as a new repository; inspect it manually. Do not
convert a v1 directory by adding the marker.

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
