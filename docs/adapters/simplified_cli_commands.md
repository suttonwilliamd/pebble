# Simplified CLI Commands for Beginners

This document describes the simplified CLI commands designed for beginners. These commands provide a more intuitive interface and abstract away some of the complexity of the core systems.

## Table of Contents

1. [Introduction](#introduction)
2. [Available Commands](#available-commands)
3. [Usage Examples](#usage-examples)
4. [Conclusion](#conclusion)

## Introduction

The simplified CLI commands are designed to be more intuitive and easier to use. They abstract away some of the complexity of the core systems and provide a more straightforward interface for common tasks. These commands are optional and can be used alongside the core CLI commands.

## Available Commands

### `pebble init`

Initialize a new Pebble repository.

**Usage:**

```bash
pebble init
```

**Description:**

This command initializes a new Pebble repository in the current directory. It sets up the necessary files and directories for the repository.

### `pebble commit`

Create a new commit with the current state of the repository.

**Usage:**

```bash
pebble commit "Commit message"
```

**Description:**

This command creates a new commit with the current state of the repository. It captures the current state of the filesystem and stores it as an immutable snapshot.

### `pebble log`

View the commit history of the repository.

**Usage:**

```bash
pebble log
```

**Description:**

This command displays the commit history of the repository. It shows the commit messages, timestamps, and other relevant information.

### `pebble push`

Push changes to a remote repository.

**Usage:**

```bash
pebble push <remote> <branch>
```

**Description:**

This command pushes changes to a remote repository. It transfers the objects and metadata to the remote repository.

### `pebble pull`

Pull changes from a remote repository.

**Usage:**

```bash
pebble pull <remote> <branch>
```

**Description:**

This command pulls changes from a remote repository. It retrieves the objects and metadata from the remote repository.

## Usage Examples

### Initialize a New Repository

```bash
mkdir my_project
cd my_project
pebble init
```

### Create a New Commit

```bash
echo "Hello, World!" > hello.txt
pebble commit "Add hello.txt"
```

### View the Commit History

```bash
pebble log
```

### Push Changes to a Remote Repository

```bash
pebble push origin main
```

### Pull Changes from a Remote Repository

```bash
pebble pull origin main
```

## Conclusion

The simplified CLI commands for beginners provide a more intuitive interface and abstract away some of the complexity of the core systems. These commands are optional and can be used alongside the core CLI commands. They are designed to make it easier for new programmers to get started with the Pebble + ROCK project.

For more information, please refer to the [documentation](docs/checklists/phase_1_core_vcs_checklist.md) and the [README](README.md).
