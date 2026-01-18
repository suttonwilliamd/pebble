# Beginner Tutorial for Pebble + ROCK

This tutorial is designed to help beginners get started with the Pebble + ROCK project. It provides step-by-step instructions for setting up the project, understanding the core concepts, and making your first contribution.

## Table of Contents

1. [Introduction](#introduction)
2. [Setting Up the Project](#setting-up-the-project)
3. [Understanding the Core Concepts](#understanding-the-core-concepts)
4. [Making Your First Contribution](#making-your-first-contribution)
5. [Next Steps](#next-steps)

## Introduction

Welcome to the Pebble + ROCK project! This tutorial will guide you through the process of setting up the project, understanding the core concepts, and making your first contribution. By the end of this tutorial, you will have a solid understanding of the project and be ready to contribute.

## Setting Up the Project

### Prerequisites

Before you begin, ensure that you have the following installed on your machine:

- Go 1.21 or later
- Git
- SQLite

### Cloning the Repository

1. Open a terminal and navigate to the directory where you want to clone the repository.

2. Clone the repository using the following command:

```bash
git clone https://github.com/yourusername/pebble.git
```

3. Navigate into the project directory:

```bash
cd pebble
```

### Building the Project

1. Build the project using the following command:

```bash
go build -o pebble ./cmd/pebble
```

2. Verify that the build was successful by running the help command:

```bash
./pebble help
```

### Running the Tests

1. Run the tests to ensure that everything is working correctly:

```bash
go test ./...
```

## Understanding the Core Concepts

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

## Making Your First Contribution

### Setting Up Your Development Environment

1. Fork the repository on GitHub.
2. Clone your fork to your local machine.
3. Set up the project as described in the [Setting Up the Project](#setting-up-the-project) section.

### Making Changes

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

## Next Steps

Congratulations! You have successfully set up the project, understood the core concepts, and made your first contribution. Here are some next steps to continue your journey with the Pebble + ROCK project:

1. Explore the [documentation](docs/checklists/phase_1_core_vcs_checklist.md) to learn more about the project.
2. Check out the [onboarding guide](docs/onboarding_guide.md) for more detailed information.
3. Join the community and start contributing to the project.

We hope that this tutorial has been helpful and that you are excited to continue your journey with the Pebble + ROCK project!

For more information, please refer to the [documentation](docs/checklists/phase_1_core_vcs_checklist.md) and the [README](README.md).
