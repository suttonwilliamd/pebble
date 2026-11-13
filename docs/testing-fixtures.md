# Disposable fixtures and shipped-behavior baselines (PB-02)

The original PB-02 measurements below qualify the then-shipped legacy APIs and
the separate ROCK pathname-index library; they are historical, not current v2
performance claims. PB-01 owns canonical v2 wire fixtures.

## Ownership and safety

`internal/testfixture` has no dependency on Pebble object or wire formats:

- `File{Path, Data}` contains a portable relative path and complete raw bytes.
- `Mixed(binaryBytes)` returns deterministic mixed files; sizes outside
  `1..16 MiB` are rejected. Total corpus size is roughly four times this
  parameter plus fixed text/image overhead, **not** `binaryBytes` in total.
- `Noise(size)` uses SHA-256 of successive little-endian uint64 counters;
  `0..16 MiB` is supported. No RNG seed, clock, network or user data is involved.
- `Revisions(binaryBytes)` returns independent slices at the same
  `binary/repeated.bin` path: original, identical, a 32-byte XOR edit at the
  midpoint, a 24-byte insertion at the midpoint, truncation to one third, empty.
  Its size parameter must be `64..16 MiB`.
- `Materialize(root, files)` writes supplied files only. Use `t.TempDir()` /
  `b.TempDir()` and a fresh child directory; it does not remove obsolete paths,
  create a repository, clean up, or make an arbitrary existing directory safe.
  Do not supply roots containing symlinks or valuable files.
- `FailAfter(data, offset, fault)` injects an `io.Reader` error at exactly that
  byte offset, including partial-read-plus-error delivery. Offset zero fails
  immediately; offset equal to length fails with the last bytes; larger offsets
  reach ordinary EOF. Negative offsets/nil errors are fixture programming errors.

Tests/benchmarks create and clean disposable repositories/databases under Go's
temporary directories. Never run against `test_dir`, the original checkout,
another worker's repository, or unrelated user repositories. No generated
corpus or database is checked in. The three frozen JSON objects total **843
bytes**; other data is generated at runtime with bounded sizes. Go 1.21 syntax
and `b.N` benchmark loops preserve the module's minimum toolchain support.

Raw-byte SHA-256 and exact lengths can be computed directly from each `File`;
they are not object-envelope hashes. `TestMixedCorpus -v` prints every path,
length and raw checksum. Text/noise/revision generation is toolchain-independent;
standard-library compressed-asset encoders can change between Go releases.
Determinism is checked within a toolchain, not by pinning incidental compressor
output as a future format contract.

## PB-12 staging and identity qualification

`go test ./cmd/pebble -run '^TestStagingIdentityCLI$' -count=1 -v` builds and runs
the actual CLI in disposable repositories. A real Git oracle compares selected
commit paths and exact bytes, including staged A versus later B, additions,
deletion/re-add, `--all`, ignored tracked modifications/deletions and untracked
exclusion. Fresh-process checkout verifies raw binary SHA-256. Config tests use
isolated HOME/config files and inspect stored author/committer provenance.
Git isolation is child `Cmd.Env` only, never persistent worker exports.

`TestIdentityConfigSerializedLimitCLI` runs actual local/global config commands
at 65,535/65,536/65,537 serialized bytes, including JSON escaping and the final
newline. Cumulative individually valid name fields and a single expanding value
cannot publish an unreadable file. Failed updates preserve previous config bytes;
subsequent CLI reads, updates and real commits still resolve recorded identities,
including from a second repository sharing the isolated global config.

`TestStagingInterruptedCommit` exits a real child at prepared/published boundaries;
reopen retains or consumes the exact index without sweeping later bytes.
`TestStagingFailsClosed`, `TestStagingIndexPublicationFailure`,
`TestIndexPublicationEveryBoundary` and `TestCommitterExtensionIdentityAndCapability`
cover locks, stale/missing/malformed records, failed restaging, physical publication
barriers, provenance and forged capability downgrades.
`TestStagingOldConsumers` compiles actual PB11 source at
`4f6c9aa256ba90f1185a4225519d7733bd10ef57` in generated
`pebble-pb12-old-*` fixtures; CI requires held/pre-lock rejection and active-writer
exclusion. Prior PB09 and v2-recovery consumers/gates remain separate and intact.
These are bounded correctness fixtures, not binary scalability or power-loss proof.

## Baseline inputs

Measured 2026-11-11 at source base `b87aee308c5580b945754d65eac0b11872abfbb2`
plus PB-02 fixture/test/benchmark additions. Production code was unchanged.
`Mixed(1 << 20)` under **Go 1.21.13** produced:

| Path | Raw file bytes | Properties |
| --- | ---: | --- |
| `empty` | 0 | Empty file |
| `tiny/one.bin` | 1 | `ff` |
| `text/readme.txt` | 67,584 | Repeated UTF-8 text |
| `text/copy.txt` | 67,584 | Identical text, independent slice |
| `nested/space name/café.txt` | 24 | Unicode/spaces in path, CRLF/LF/NUL bytes |
| `binary/random.bin` | 1,048,576 | Deterministic high entropy |
| `binary/copy.bin` | 1,048,576 | Identical binary, independent slice |
| `binary/zeros.bin` | 1,048,576 | Dense zero bytes, not a sparse file |
| `assets/scene.png` | 35,237 | Valid 128x128 noisy RGB gradient, lossless |
| `assets/scene.jpg` | 3,981 | Same generated scene, JPEG quality 85 |
| `assets/readme.txt.gz` | 339 | Valid gzip of `text/readme.txt`, no timestamp/name |
| `assets/bundle.zip` | 1,049,470 | Valid deflated ZIP: `readme.txt` + `payload.bin` |
| **Total** | **4,369,948** | **12 files** |

These are actual decodable/compressed assets, not random bytes renamed `.png`
or `.zip`. They are synthetic and modest, not a representative production
media survey. The ZIP intentionally contains high-entropy payload that does not
compress well; text/gzip/zero data cover the opposite extreme. Tests fully
decode both images and compare decompressed archive entries with original bytes.

Useful Go 1.21.13 raw-file checksums:

| Input | SHA-256 |
| --- | --- |
| `binary/random.bin` / `binary/copy.bin` | `8936491f7e7dd3ca297960ec425e8375f1b9db51278d5fff5481205c0992a132` |
| `text/readme.txt` / `text/copy.txt` | `e176cc89094d1289dccce1c38fc8340e1dc54a165205c13cd646554dfac2e936` |
| `assets/scene.png` | `0826d4bffc7cb3b26e64f5f6682b3bf68ae94dbb5c425632a1fdc92ae4953de9` |
| `assets/scene.jpg` | `52abf1ea5481b49131b15253a457d9705a8ad0b21d3d1002e43264553c9105b7` |
| `assets/bundle.zip` | `2ddc1558a54c6a6a551e681983f2d957be5a0d1adcc56d05840f8f62a959631f` |

`Revisions(1 << 20)` submits six states totaling **4,543,853 bytes**:
1,048,576; 1,048,576; 1,048,576; 1,048,600; 349,525; 0.
The snapshot history test uses `Mixed(64 << 10)` plus `Revisions(64 << 10)` to
keep routine test cost bounded. Each changed snapshot is reopened and checked
out in reverse order; every committed object and restored file is compared to
its expected complete bytes. Identical content must not advance the branch.

## Compatibility and fault coverage

`internal/snapshot/testdata/compat-v1/{blob,tree,commit}.json` is a tiny synthetic
repository frozen in the current JSON/base64 persisted-object format. It was
constructed independently of `StoreObject`/`CreateCommit`, with fixed identity,
time and raw SHA-256 addresses; it is **not** a claim of compatibility with an
unavailable historical release/export. The test writes these exact envelopes
into a disposable repository, reopens it, checks out binary NUL/`ff`/CRLF bytes,
verifies it, then commits new history with the frozen commit as parent. It does
not compare today's serialization spelling or key order.

| Case | Consumer-visible check |
| --- | --- |
| Frozen current JSON repository | Exact restored binary bytes, graph verification, continued history |
| Mixed-file/same-path snapshot revisions | Exact stored and restored bytes for each history state after reopening |
| Truncated JSON, invalid base64, negative/wrong size, wrong address/content, unknown type, null/missing fields | Malformed object rejected; storing valid data cannot hide existing corruption |
| Malformed SQLite header/body | Database initialization rejects malformed input |
| Missing referenced chunk | Reconstruction reports the missing hash rather than success |
| Reader failure at offsets 0/1/8192/8193/20000/end | Error preserved; emitted chunks remain a genuine input prefix; later offsets exercise partial progress |
| Callback failure on second streamed chunk | Original error returned, no subsequent callback |
| Blob reader failure, including final bytes plus error | No partial blob published |
| SQLite trigger abort on second reference | Earlier inserted chunk/ref rolls back, prior durable refs unchanged, retry reconstructs exact bytes |

The SQLite trigger is test-local (`RAISE(ABORT, ...)`) and requires no production
fault-injection hook. Existing snapshot safety tests also cover portable object
write obstruction, writer lock exclusion, corrupt-checkout preflight and unsafe
paths. These deterministic failures are **not** power-loss, full-disk, physical
I/O, durable-checkout rollback or cancellation qualification. Current
`ReconstructFile` can leave partial/truncated output on failure; the missing
chunk test only qualifies error reporting, not atomic destination replacement.

## Reproduce measurements

Run from the isolated Pebble worktree with disposable temp storage available:

```sh
# Set GO to an installed executable; no installation/download is needed.
GO='C:/Users/william64/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.21.13.windows-amd64/bin/go.exe'
GOTOOLCHAIN=local "$GO" version
GOTOOLCHAIN=local "$GO" env GOOS GOARCH CGO_ENABLED
GOTOOLCHAIN=local "$GO" test ./internal/testfixture -run '^TestMixedCorpus$' -count=1 -v
GOTOOLCHAIN=local "$GO" test ./internal/rock ./internal/snapshot -run '^$' \
  -bench '^BenchmarkFixture' -benchmem -benchtime=3x -count=3 -cpu=1 -p=1
```

Three independent samples of three operations each; benchmark packages run
serially (`-p=1`), with `GOMAXPROCS=1` (`-cpu=1`). The host is Windows 11 Home
10.0.26300, amd64, AMD Ryzen AI 9 HX 370, 12 cores/24 logical processors;
`CGO_ENABLED=0`. Toolchain: **go1.21.13 windows/amd64**, SQLite dependency
`modernc.org/sqlite v1.29.10`. Files are in the host's local temporary directory.
No cache flush, CPU affinity, power-plan control or exclusive host was used.
No manual warmup was performed. Go 1.21's benchmark harness performs its normal
initial single-iteration probe (`testing.B.run1`) before the fixed-count measured
run; that probe is not one of the reported samples. Each benchmark's explicit
setup/timer boundaries are described below. These are **small-corpus baselines,
not large-binary qualification**.

### Measured Go 1.21.13 results

Time is median with observed min–max across three samples, in **milliseconds
per operation**. Allocation bytes are medians of Go's `B/op` counters, not RSS.
No statistical significance or throughput extrapolation is claimed.

| Operation/input | Median ms/op | Min–max ms/op | Allocated B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Chunk mixed / batch | 38.636 | 38.553–40.489 | 4,844,082 | 2,880 |
| Chunk mixed / stream | 44.022 | 43.113–48.247 | 8,725,170 | 8,674 |
| Chunk six revisions / batch | 13.477 | 13.241–14.087 | 5,168,528 | 3,425 |
| Chunk six revisions / stream | 23.769 | 23.420–24.662 | 10,550,360 | 10,652 |
| Chunk 1 MiB entropy / batch | 2.797 | 2.673–2.982 | 1,191,632 | 792 |
| Chunk 1 MiB entropy / stream | 5.398 | 5.341–5.714 | 2,441,432 | 2,476 |
| Chunk 1 MiB zeros / batch | 22.901 | 22.795–23.974 | 1,080,784 | 392 |
| Chunk 1 MiB zeros / stream | 24.034 | 22.878–40.991 | 1,113,280 | 1,022 |
| Chunk PNG / batch | 0.176 | 0.168–0.190 | 40,400 | 32 |
| Chunk PNG / stream | 0.358 | 0.177–0.361 | 74,336 | 62 |
| Chunk ZIP / batch | 5.034 | 4.601–6.352 | 1,193,424 | 792 |
| Chunk ZIP / stream | 9.116 | 8.606–9.731 | 2,440,320 | 2,472 |
| SQLite 1 MiB ingest | 11.941 | 11.396–13.285 | 160,552 | 6,057 |
| Initial mixed snapshot commit | 131.346 | 128.422–133.153 | 17,404,909 | 1,204 |
| Snapshot checkout pair | 369.878 | 329.848–383.500 | 47,938,200 | 4,665 |

JPEG's 3-iteration samples emitted no `ns/op`/MB/s on this timer; they were not
zero-cost. A targeted **Go 1.21.13** follow-up used 1,000 operations per sample:

```sh
GOTOOLCHAIN=local "$GO" test ./internal/rock -run '^$' \
  -bench 'BenchmarkFixtureChunks/assets/scene[.]jpg' -benchmem \
  -benchtime=1000x -count=3 -cpu=1 -p=1
```

| JPEG operation | Median µs/op | Min–max µs/op | Allocated B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Batch | 6.431 | 6.380–7.272 | 4,288 | 4 |
| Stream | 7.462 | 7.025–8.515 | 13,808 | 6 |

This follow-up resolves the short-sample timer gap, not a cross-toolchain comparison.

Timed boundaries and storage counters:

- Chunk benchmarks exclude input generation, disk reads, validation and database
  storage. Stream callbacks discard chunks; they do not retain all output.
  Reported `chunks/op`/`unique-B/op` are input-analysis counters across the entire
  group, not persisted repository savings: mixed **942 / 1,488,043**; revisions
  **1,127 / 1,059,810**; entropy **261 / 1,048,576**; zeros **128 / 8,192**;
  PNG **9 / 35,237**; JPEG **1 / 3,981**; ZIP **261 / 1,049,470**.
- SQLite ingest times one `AddFileChunks` transaction in a fresh database;
  generation, chunking, schema initialization, close and deletion are excluded.
  Database file after close: **1,245,184 bytes** for 1,048,576 raw payload bytes
  and 261 refs. File length is not filesystem allocation or a whole-repo size.
- Initial snapshot commit times only `Repository.Commit` on a fresh mixed
  corpus. Init/materialization/verification/cleanup are excluded. Stored JSON
  object-file lengths total **4,342,311 bytes**, with duplicate raw content
  naturally sharing object addresses. This is not a ROCK-backed commit.
- Checkout times **two** operations (original then edited) in a prepared mixed
  repository with an additional 1 MiB repeated path; setup commits are excluded.
  Each checkout has 13 files / **5,418,524 logical bytes**; `SetBytes` counts
  **10,837,048 bytes per pair**, not changed bytes or actual disk I/O. Both states
  have equal length. Their JSON object files total **5,743,701 bytes**; this is a
  two-history footprint, not an incremental write-byte measurement.

## Measurement gaps

This Windows run does not establish Linux/macOS behavior or cross-toolchain
performance. No v2 implementation was measured. Unmeasured: peak RSS, syscall
counts, actual physical read/write amplification, cold-cache performance,
large real-world media, multi-GiB files/repos, long revision histories,
network/remote throughput, Git comparisons, cancellation and crash durability.
Go allocation counters include total allocations throughout an operation;
streaming's allocation total must not be presented as its peak retained memory.
The short fixed-count samples expose substantial host/timer variance and are
baseline observations, not performance gates or optimization targets.

## Repeated-path ROCK defect: expected versus observed

This is a **known defect**, not a compatibility requirement or a qualified
revision-history path. No production fix is part of PB-02. The reusable revision
corpus is suitable for downstream manifest/storage/integration work.

A disposable overlay probe under **Go 1.27.3 windows/amd64, CGO_ENABLED=0**
submitted original and edited `Revisions(64 << 10)` bytes at the same file path.
Exact path: `DefaultFastCDC().Chunk` → `ChunkDatabase.AddFileChunks`
(`internal/rock/database.go`, SQLite `chunks` / `chunk_refs` tables in a temporary
`chunks.db`) → `ChunkGenerator.ReconstructFile`
(`internal/rock/generator.go`, via `GetChunkRefs` / `GetChunk`). It does not use
`GenerateChunks`' file-selection heuristic or a v2 manifest.

This is the currently **unwired ROCK library**, not a shipped CLI command path:
`cmd/pebble/main.go` exposes no ROCK operation, and snapshot `Repository.Commit`
uses `CreateBlob` / `StoreObject` for whole-file JSON/base64 objects. The probe
does not establish a CLI snapshot history defect. Observed library results:

| State | Submitted chunks | Stored path refs | Expected bytes | Observed bytes |
| --- | ---: | ---: | ---: | ---: |
| Original | 16 | 16 | 65,536 | 65,536 |
| Edited | 16 | 32 | 65,536 | 131,072 |

- Original expected and restored SHA-256:
  `c15b51264ecf9cb11885fbd148da5917dd7968606228f5a2a4cd78b77a949b04`.
- Edited expected SHA-256:
  `1c51c87db6db4a09aebd498872d0aa9b0665cfd20965c782a9413e65a9b60a02`.
- Edited observed restored SHA-256:
  `a24c1be6ed886248569bf53534dde5d7bc4a0ba4d6982944cf630b36d4cb77c9`.

References accumulate and reconstruction mixes both submissions. Only
`chunk_offset` orders refs, so equal-offset tie order is not guaranteed; the
observed wrong-output hash is evidence from this run, not a portable expected
hash. Chunk refs have no revision selector, so this also does not demonstrate
independent retrieval of both historical versions. Snapshot history tests do
verify historical bytes, but snapshots currently store whole-file blobs and
do not use this defective ROCK revision path.

### Disposable reproduction without adding a permanent defect-pinning test

Save the following as `probe_test.go` in a new **system temp** directory. It
logs observations rather than asserting that broken accumulation must persist.

```go
package rock

import (
    "crypto/sha256"
    "os"
    "path/filepath"
    "testing"

    "github.com/suttonwilliamd/pebble/internal/testfixture"
)

func TestPB02RepeatedPathProbe(t *testing.T) {
    root := t.TempDir()
    db, err := NewChunkDatabase(filepath.Join(root, "chunks.db"))
    if err != nil { t.Fatal(err) }
    defer db.Close()
    revisions := testfixture.Revisions(64 << 10)
    for _, revision := range []testfixture.Revision{revisions[0], revisions[2]} {
        chunks, err := DefaultFastCDC().Chunk(revision.File.Data)
        if err != nil { t.Fatal(err) }
        if err := db.AddFileChunks(revision.File.Path, chunks); err != nil { t.Fatal(err) }
        refs, err := db.GetChunkRefs(revision.File.Path)
        if err != nil { t.Fatal(err) }
        output := filepath.Join(root, "restored.bin")
        if err := NewChunkGenerator(db).ReconstructFile(revision.File.Path, output); err != nil { t.Fatal(err) }
        got, err := os.ReadFile(output)
        if err != nil { t.Fatal(err) }
        t.Logf("%s: submitted=%d chunks, stored=%d refs, want=%d bytes sha256=%x, got=%d bytes sha256=%x",
            revision.Name, len(chunks), len(refs), len(revision.File.Data),
            sha256.Sum256(revision.File.Data), len(got), sha256.Sum256(got))
    }
}
```

Create `overlay.json` alongside it, replacing the example absolute paths with
the actual isolated worktree and temp directory. Use forward slashes on Windows:

```json
{"Replace":{"C:/isolated/pebble/internal/rock/pb02_probe_test.go":"C:/system-temp/pb02/probe_test.go"}}
```

From the isolated worktree, run the installed Go executable:

```sh
GOTOOLCHAIN=local "$GO" test -overlay=C:/system-temp/pb02/overlay.json \
  ./internal/rock -run '^TestPB02RepeatedPathProbe$' -count=1 -v
```

The overlay creates no source file in the worktree. Test databases/restored
files are removed by `t.TempDir`; remove the caller-owned probe/overlay temp
directory after use. No fixture asserts the observed corruption as desirable.

## Executed verification

Installed tool identities were checked with `go version`; directory labels were
not treated as version evidence. No toolchains were installed or persistent
environment settings changed.

- Minimum executable:
  `C:/Users/william64/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.21.13.windows-amd64/bin/go.exe`
  reported `go version go1.21.13 windows/amd64`; `CGO_ENABLED=0`.
- Preferred executable:
  `C:/Users/william64/tools/PebbleGo-1.27.3/go/bin/go.exe`
  reported `go version go1.27.3 windows/amd64`; `CGO_ENABLED=0`.
  Companion `gofmt.exe -h` and the new-file `gofmt.exe -l` check succeeded;
  the latter reported no unformatted files.
- Go 1.21.13 focused fixture checks, passed:

  ```sh
  go test ./internal/testfixture ./internal/rock ./internal/snapshot \
    -run 'Test(MixedCorpus|RevisionsPreserveIndependentHistory|CorpusBoundsAndContainment|FailAfterPreservesPartialReadError|Fixture)' \
    -count=1 -v
  go test ./internal/rock \
    -run 'TestFixture(MalformedSQLite|MissingChunkFailsReconstruction)' \
    -count=1 -v
  ```

- Go 1.21.13 benchmark commands above, passed; timings in this document belong
  exclusively to that toolchain.
- Go 1.27.3 overlay probe above, passed as a diagnostic/logging run; its output
  demonstrates the defect, not correct versioned reconstruction.
- Go 1.27.3 applicable existing checks, all passed using the preferred absolute
  executable and `GOTOOLCHAIN=local` on each command:

  ```sh
  go mod verify
  go build ./...
  go vet ./...
  go test ./... -count=1
  ```

The final suite passed all test-bearing packages, including CLI, snapshots,
ROCK, remote, storage, sync protocol and the new fixture package; `cmd/pebbled`
has no tests. Race/fuzz campaigns, Docker and other operating systems were not
run as part of PB-02. This is local evidence only; nothing was published.

For exact environment provenance: the first Go 1.21.13 focused checks and
3-iteration benchmark command used the cached absolute executable without
overriding `GOTOOLCHAIN`; the observed executable version was Go 1.21.13.
The reproduction recipes above explicitly set `GOTOOLCHAIN=local` to prevent
downloads. The targeted JPEG follow-up and Go 1.27.3 final checks used that
override in the executed commands.

## PB-08 migration qualification additions

`internal/testfixture/v1` copies frozen compatibility payloads into caller-owned
temporary roots, then generates deterministic shared-ancestor/divergent branches
and PB-02 mixed/repeated-path histories using shipped v1 snapshot structs and
object storage rules. Checked-in fixture directories are never migration roots.
Working sentinels and an unrelated index cache prove conversion uses stored history.

`TestMigration*` and `TestExecutableMigration` compare restored bytes/checksums,
identity/date/message/parent/tree/ref mappings and complete selected closure.
Source fingerprints include bytes, refs/HEAD, working state, modes, directory
entries and modification times; access times are excluded because reads may update
them. PB-02 fail-after readers produce truncated on-disk envelope fixtures.
Other cases cover missing/corrupt/oversized objects, unsupported metadata/modes,
empty history, unsafe paths/aliases/overlap, existing destinations, cancellation,
competing publication and process termination before destination installation.
Writable repositories/destinations and interrupted stages belong to test temp
directories, never `test_dir`, code checkouts as input, user repositories or services.

