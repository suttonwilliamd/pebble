# Example Projects for Beginners

This document describes the example projects designed for beginners. These projects demonstrate how to use the core systems in practice and provide step-by-step instructions and explanations of key concepts.

## Table of Contents

1. [Introduction](#introduction)
2. [Available Example Projects](#available-example-projects)
3. [Usage Examples](#usage-examples)
4. [Conclusion](#conclusion)

## Introduction

The example projects are designed to demonstrate how to use the core systems in practice. They include step-by-step instructions and explanations of key concepts, making it easier for beginners to understand and contribute to the project. These projects are optional and can be used alongside the core documentation.

## Available Example Projects

### Basic Repository Setup

This example project demonstrates how to set up a basic Pebble repository and make your first commit.

**Directory:**

```bash
examples/basic_repository_setup
```

**Description:**

This project shows how to initialize a new Pebble repository, create a new commit, and view the commit history.

### Binary Asset Handling

This example project demonstrates how to handle binary assets using the ROCK Binary System.

**Directory:**

```bash
examples/binary_asset_handling
```

**Description:**

This project shows how to add binary files to a Pebble repository, commit them, and view the commit history.

### Conflict Resolution

This example project demonstrates how to resolve conflicts using the Combine Operation.

**Directory:**

```bash
examples/conflict_resolution
```

**Description:**

This project shows how to create a conflict, use the Combine Operation to resolve it, and commit the resolved changes.

### Remote Sync

This example project demonstrates how to sync changes with a remote repository using the Remote Sync Protocol.

**Directory:**

```bash
examples/remote_sync
```

**Description:**

This project shows how to push changes to a remote repository and pull changes from a remote repository.

## Usage Examples

### Basic Repository Setup

1. Navigate to the example project directory:

```bash
cd examples/basic_repository_setup
```

2. Initialize a new Pebble repository:

```bash
pebble init
```

3. Create a new commit:

```bash
pebble commit "Initial commit"
```

4. View the commit history:

```bash
pebble log
```

### Binary Asset Handling

1. Navigate to the example project directory:

```bash
cd examples/binary_asset_handling
```

2. Initialize a new Pebble repository:

```bash
pebble init
```

3. Add a binary file to the repository:

```bash
pebble add binary_file.bin
```

4. Create a new commit:

```bash
pebble commit "Add binary file"
```

5. View the commit history:

```bash
pebble log
```

### Conflict Resolution

1. Navigate to the example project directory:

```bash
cd examples/conflict_resolution
```

2. Initialize a new Pebble repository:

```bash
pebble init
```

3. Create a new commit:

```bash
pebble commit "Initial commit"
```

4. Make changes to create a conflict:

```bash
echo "Conflict line" > conflict.txt
pebble add conflict.txt
pebble commit "Add conflict.txt"
```

5. Use the Combine Operation to resolve the conflict:

```bash
pebble combine
```

6. Commit the resolved changes:

```bash
pebble commit "Resolve conflict"
```

### Remote Sync

1. Navigate to the example project directory:

```bash
cd examples/remote_sync
```

2. Initialize a new Pebble repository:

```bash
pebble init
```

3. Create a new commit:

```bash
pebble commit "Initial commit"
```

4. Push changes to a remote repository:

```bash
pebble push origin main
```

5. Pull changes from a remote repository:

```bash
pebble pull origin main
```

## Conclusion

The example projects for beginners demonstrate how to use the core systems in practice. They include step-by-step instructions and explanations of key concepts, making it easier for beginners to understand and contribute to the project. These projects are optional and can be used alongside the core documentation.

For more information, please refer to the [documentation](docs/checklists/phase_1_core_vcs_checklist.md) and the [README](README.md).
