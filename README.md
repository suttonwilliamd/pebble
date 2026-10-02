# Pebble

Pebble is a binary-first Git replacement (the next Git). Designed for correctness, immutability, and superior binary asset handling at scale.

## Why Pebble

| Problem | Pebble Solution |
|---------|-----------------|
| Git stores full snapshots, bloat on large binaries | Content-defined chunking (ROCK) with deduplication |
| Binaries treated as opaque blobs | First-class binary diffing and storage |
| Large repos slow to clone/push | HTTP/2 + content-addressed transfer + resumable uploads |
| Inline conflict markers (`<<<<<<`) | Materialized conflict files, explicit resolution |
| No built-in access control | Basic RBAC for team collaboration |

**Bottom line**: If you ship binaries, media, or datasets—Pebble is purpose-built for you.

## Roadmap to Next Git

See [docs/next-git-roadmap.md](docs/next-git-roadmap.md) for phased deliverables and metrics.

### Phase 1: Core VCS (In Progress)
- [x] Snapshot system (immutable commits)
- [x] ROCK binary chunking
- [ ] Combine/conflict engine
- [ ] Content-addressed storage (CAS)
- [ ] Remote sync protocol

### Phase 2: Collaboration
- Delta snapshots
- Git interoperability (import/export)
- Enhanced conflict resolution
- Performance benchmarking

### Phase 3: Enterprise
- RBAC / access control
- Tiered storage
- IDE integrations
- Self-hosted server

### Core Features

- **Immutable History**: All commits are snapshot-based and immutable, ensuring a reliable and tamper-proof history.
- **Binary Asset Handling**: First-class support for binary assets using content-defined chunking, optimized for storage and transfer.
- **Conflict Resolution**: Materialized conflict files for clear and actionable conflict resolution, avoiding inline markers.
- **Efficient Storage**: Content-addressed storage with deduplication and garbage collection, minimizing storage requirements.
- **Remote Sync**: HTTP/2-based remote sync protocol with resumable transfers and secure authentication (SSH and HTTPS with tokens).

### Advanced Features

- **Delta Snapshots**: Store only the differences between snapshots, optimizing performance and reducing storage requirements.
- **Enhanced Conflict Resolution**: Improved conflict detection and resolution with a user-friendly interface and better conflict handling.
- **Git Interoperability**: Tools to import Git repositories into Pebble and export Pebble repositories to Git, ensuring compatibility with existing Git workflows.
- **Performance Optimization**: Benchmarked and optimized critical operations, including snapshot creation, retrieval, chunking, deduplication, and remote sync.
- **Access Control**: Basic role-based access control (RBAC) to restrict access based on user roles, ensuring secure collaboration.

## Architecture

### Core Systems

- **Snapshot System**: Immutable, content-addressed snapshots representing complete filesystem states.
- **ROCK Binary System**: Content-defined chunking for efficient binary asset handling.
- **Combine Engine**: Explicit conflict materialization for clear and actionable conflict resolution.

### Storage Layer

- **Content-Addressed Storage**: Objects stored using SHA-256 hashes for unique identification and direct access.
- **SQLite Metadata Database**: Efficient storage and retrieval of metadata for objects and snapshots.
- **Two-Tier Cache**: Memory and disk cache for fast access to frequently used objects.
- **Reference Counting**: Track references to objects, enabling garbage collection.
- **Garbage Collection**: Identify and remove unreferenced objects, freeing up storage space.

### Remote Sync Protocol

- **HTTP/2 Protocol**: Efficient communication with multiplexed requests and responses.
- **Content-Addressed Transfer**: Transfer objects using content-addressed identifiers for direct access.
- **Resumable Transfers**: Support for resumable transfers, handling network interruptions gracefully.
- **SSH Authentication**: Secure authentication using SSH keys.
- **HTTPS with Tokens**: Secure authentication using HTTPS with tokens.

### CLI Interface

- **Commit**: Create a new commit with the current state of the repository.
- **Status**: Show the current status of the repository, displaying modified, added, and deleted files.
- **Log**: Show the commit history of the repository, displaying commit messages and timestamps.
- **Branch**: Manage branches in the repository, including creation, deletion, and listing.
- **Checkout**: Switch to a different branch or snapshot, updating the working directory.
- **Push**: Push changes to a remote repository, transferring objects and metadata.
- **Pull**: Pull changes from a remote repository, retrieving objects and metadata.

## Getting Started

### Prerequisites

- Go 1.21.x (minimum supported toolchain)
- Git
- SQLite (runtime only; driver is bundled via pure-Go modernc.org/sqlite)

### Platform caveats

- Go 1.19/1.20 are not supported.
- CI validates on Go 1.21.x (`.github/workflows/ci.yml`).
- If you hit toolchain issues, run:

```bash
go version
go mod tidy
go test ./...
```

### Installation

1. Clone the repository:

```bash
git clone https://github.com/suttonwilliamd/pebble.git
cd pebble
```

2. Build the project:

```bash
go build -o pebble ./cmd/pebble
```

3. Run the tests:

```bash
go test ./...
```

### Usage

1. Initialize a new Pebble repository:

```bash
pebble init
```

2. Create a new commit:

```bash
pebble commit "Initial commit"
```

3. Inspect actual working-tree changes:

```bash
pebble status
```

Status reports the current branch and **added**, **modified**, and **deleted**
regular files relative to that branch's HEAD snapshot. Binary changes are detected
by content hash, not just file size or modification time. Paths are quoted so
spaces and control characters are unambiguous. A committed, unchanged repository
reports `Working tree clean`; an unborn branch reports `No commits yet` and its
files to commit. Status is read-only and fails on invalid/missing HEAD or tree
objects instead of reporting a misleading clean state. Run it at the repository
root; it does not implicitly initialize a repository or discover parent repos.

#### Ignore rules shared by status and commit

Place rules in the root `.pebbleignore` (not `.gitignore`). Both commands use the
same rules. `.pebble` and `.git` metadata are always excluded, even without an
ignore file. Already tracked files remain tracked when a new ignore rule matches
them; their modifications and deletions are still reported and committed.

```text
# Comments and blank lines are allowed.
*.log
build/
.env
/root-only.txt
assets/*.tmp
```

- Patterns are case-sensitive and use `/` separators on every platform.
- A basename pattern matches at any depth; `build/` excludes directories named
  `build` and their contents, but not a regular file named `build`.
- A leading `/` anchors a pattern at the repository root. Patterns containing
  `/`, such as `assets/*.tmp`, are also root-relative.
- `*`, `?`, and bracket classes are supported. `*` does not cross `/` boundaries.
- Blank lines and lines beginning with `#` are ignored; surrounding whitespace is
  trimmed. CRLF files work. Nested ignore files are not read.
- Negation (`!`) and recursive `**` patterns are **not supported**; unsupported or
  malformed patterns cause both commands to fail rather than silently ignore
  the rule. Inline comments are not supported.
- Symlinks and special files are not snapshotted. Replacing a tracked regular
  file with a symlink reports the tracked file as deleted without following the
  link. Executable-bit changes are detected where the filesystem supports them;
  other permission and mtime-only changes are not treated as content changes.
- Newly committed paths use `/` separators. Historical Windows tree paths are
  recognized. Literal backslashes and colons in POSIX filenames are rejected
  instead of creating snapshots that cannot be read back portably.

**Current checkout limitation:** `pebble checkout` changes HEAD but does not yet
restore working files. Status deliberately compares with the selected branch's
HEAD, so it correctly shows those remaining differences rather than using the
previous branch's saved index. This change does not implement checkout restoration
or connect the CLI snapshot format to ROCK chunk storage.

4. View the commit history:

```bash
pebble log
```

5. Push changes to a remote repository:

```bash
pebble push origin main
```

## Documentation

- [Phase 1: Core VCS Development Plan Checklist](docs/checklists/phase_1_core_vcs_checklist.md)
- [Phase 2: Collaboration Checklist](docs/checklists/phase_2_collaboration_checklist.md)
- [Phase 3: Enterprise Checklist](docs/checklists/phase_3_enterprise_checklist.md)

## Contributing

Contributions are welcome! Please read the [CONTRIBUTING.md](CONTRIBUTING.md) for details on how to contribute to this project.

## Acknowledgments

- Inspired by Git and other modern version control systems
- Built with Go for performance and reliability
- Designed for correctness, immutability, clarity, and continuous progress
