# Onboarding Guide for New Programmers

This guide is designed to help new programmers get started with the Pebble + ROCK project. It provides an overview of the project structure, key concepts, and step-by-step instructions for setting up and contributing to the project.

## Table of Contents

1. [Project Overview](#project-overview)
2. [Getting Started](#getting-started)
3. [Understanding the Core Systems](#understanding-the-core-systems)
4. [Contributing to the Project](#contributing-to-the-project)
5. [Optional Adapters for Beginners](#optional-adapters-for-beginners)

## Project Overview

Pebble + ROCK is a modern version control system with first-class binary asset handling. It is designed with a focus on correctness, immutability, clarity, and continuous progress. The project is structured into several core systems:

- **Snapshot System**: Immutable, content-addressed snapshots representing complete filesystem states.
- **ROCK Binary System**: Content-defined chunking for efficient binary asset handling.
- **Combine Operation**: Explicit conflict materialization for clear and actionable conflict resolution.
- **Storage Layer**: Content-addressed storage with deduplication and garbage collection.
- **Remote Sync Protocol**: HTTP/2-based remote sync protocol with resumable transfers and secure authentication.

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

## Understanding the Core Systems

### Snapshot System

The Snapshot System is responsible for creating immutable, content-addressed snapshots of the filesystem. It includes components like the File Index Generator, Content Addressing, Metadata Generator, and Object Database.

### ROCK Binary System

The ROCK Binary System handles binary assets using content-defined chunking. It includes components like the File Analyzer, FastCDC Algorithm, Chunk Generator, and Deduplication Engine.

### Combine Operation

The Combine Operation is responsible for conflict resolution. It includes components like the Snapshot Comparator, Tree Walker, File Comparator, and Resolution Tracker.

### Storage Layer

The Storage Layer provides content-addressed storage with deduplication and garbage collection. It includes components like the SQLite Metadata Database, Two-Tier Cache, and Reference Counting.

### Remote Sync Protocol

The Remote Sync Protocol enables efficient communication with remote repositories. It includes components like the HTTP/2 Protocol, Content-Addressed Transfer, and Resumable Transfers.

## Contributing to the Project

### Setting Up Your Development Environment

1. Fork the repository on GitHub.
2. Clone your fork to your local machine.
3. Set up the project as described in the [Installation](#installation) section.

### Making Contributions

1. Create a new branch for your feature or bug fix:

```bash
git checkout -b feature/your-feature-name
```

2. Make your changes and commit them:

```bash
git commit -m "Add your commit message"
```

3. Push your changes to your fork:

```bash
git push origin feature/your-feature-name
```

4. Open a pull request on GitHub.

### Running Tests

Before submitting your changes, ensure that all tests pass:

```bash
go test ./...
```

## Optional Adapters for Beginners

### Simplified CLI Commands

For beginners, we provide simplified CLI commands that abstract away some of the complexity of the core systems. These commands are designed to be more intuitive and easier to use.

### Example Projects

We provide example projects that demonstrate how to use the core systems in practice. These projects include step-by-step instructions and explanations of key concepts.

### Tutorials

We offer tutorials that guide new programmers through the process of setting up and using the project. These tutorials include hands-on exercises and examples.

## Conclusion

This onboarding guide is designed to help new programmers get started with the Pebble + ROCK project. It provides an overview of the project structure, key concepts, and step-by-step instructions for setting up and contributing to the project. We hope that this guide will make it easier for new programmers to understand and contribute to the project.

For more information, please refer to the [documentation](docs/checklists/phase_1_core_vcs_checklist.md) and the [README](README.md).
