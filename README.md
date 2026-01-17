# Pebble + ROCK

Pebble + ROCK is a modern version control system with first-class binary asset handling. It is designed with a focus on correctness, immutability, clarity, and continuous progress.

## Features

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

- Go 1.21 or later
- Git
- SQLite

### Installation

1. Clone the repository:

```bash
git clone https://github.com/yourusername/pebble.git
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

3. View the commit history:

```bash
pebble log
```

4. Push changes to a remote repository:

```bash
pebble push origin main
```

## Documentation

- [Phase 1: Core VCS Development Plan Checklist](docs/checklists/phase_1_core_vcs_checklist.md)
- [Phase 2: Collaboration Checklist](docs/checklists/phase_2_collaboration_checklist.md)
- [Phase 3: Enterprise Checklist](docs/checklists/phase_3_enterprise_checklist.md)

## Contributing

Contributions are welcome! Please read the [CONTRIBUTING.md](CONTRIBUTING.md) for details on how to contribute to this project.

## License

This project is licensed under the MIT License. See the [LICENSE.md](LICENSE.md) file for details.

## Acknowledgments

- Inspired by Git and other modern version control systems
- Built with Go for performance and reliability
- Designed for correctness, immutability, clarity, and continuous progress
