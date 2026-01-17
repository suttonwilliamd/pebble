# Snapshot System Module

## Overview

The Snapshot System is the core of Pebble's version control. It captures the state of the repository at a point in time, storing all files and metadata in a content-addressed format.

## Key Components

### 1. File Index Generator

- **Purpose**: Traverses the repository directory structure and generates an index of all files.
- **Inputs**: Repository root directory
- **Outputs**: List of files with paths and metadata
- **Dependencies**: None

### 2. Content Addressing

- **Purpose**: Generates SHA-256 hashes for file contents to ensure data integrity and enable deduplication.
- **Inputs**: File contents
- **Outputs**: Content hash
- **Dependencies**: SHA-256 library

### 3. Metadata Generator

- **Purpose**: Creates metadata for each file, including timestamps, permissions, and other attributes.
- **Inputs**: File index, content hashes
- **Outputs**: Metadata objects
- **Dependencies**: File Index Generator, Content Addressing

### 4. Object Database (SQLite)

- **Purpose**: Stores snapshot metadata and references to content-addressed objects.
- **Inputs**: Metadata objects, content hashes
- **Outputs**: Stored objects in SQLite database
- **Dependencies**: SQLite library

### 5. Reference Counter

- **Purpose**: Tracks references to objects for garbage collection.
- **Inputs**: Object references
- **Outputs**: Updated reference counts
- **Dependencies**: Object Database

### 6. Index (SQLite)

- **Purpose**: Provides fast lookups for objects and snapshots.
- **Inputs**: Object and snapshot metadata
- **Outputs**: Indexed data for quick retrieval
- **Dependencies**: SQLite library

## Performance Considerations

- **SQLite for fast local queries**: Ensures quick access to metadata and references.
- **Memory-mapped file access**: Improves performance for large files.
- **Lazy loading of object contents**: Reduces memory usage by loading only what is needed.
- **Batch object retrieval**: Optimizes performance for bulk operations.

## Testing Strategy

- **Unit Tests**: Test each component in isolation (e.g., File Index Generator, Content Addressing).
- **Integration Tests**: Test interactions between components (e.g., File Index Generator + Content Addressing).
- **Performance Tests**: Benchmark snapshot creation and retrieval times.
- **Stress Tests**: Test with large repositories to ensure scalability.

## Success Criteria

- All unit tests pass.
- Integration tests confirm components work together seamlessly.
- Performance benchmarks meet targets (< 2s for 10K files).
- Stress tests validate scalability.
