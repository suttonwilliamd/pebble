# Beginner Adapters for Pebble + ROCK

This document describes optional adapters designed to simplify the Pebble + ROCK experience for beginners. These adapters provide a more intuitive interface and abstract away some of the complexity of the core systems.

## Table of Contents

1. [Introduction](#introduction)
2. [Simplified CLI Commands](#simplified-cli-commands)
3. [Example Projects](#example-projects)
4. [Tutorials](#tutorials)
5. [Conclusion](#conclusion)

## Introduction

The Pebble + ROCK project is designed with a focus on correctness, immutability, clarity, and continuous progress. However, the complexity of the core systems can be overwhelming for beginners. To address this, we provide optional adapters that simplify the experience and make it easier for new programmers to get started.

## Simplified CLI Commands

### Overview

The simplified CLI commands are designed to be more intuitive and easier to use. They abstract away some of the complexity of the core systems and provide a more straightforward interface for common tasks.

### Available Commands

1. **`pebble init`**: Initialize a new Pebble repository.
2. **`pebble commit`**: Create a new commit with the current state of the repository.
3. **`pebble log`**: View the commit history of the repository.
4. **`pebble push`**: Push changes to a remote repository.
5. **`pebble pull`**: Pull changes from a remote repository.

### Usage Examples

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

5. Pull changes from a remote repository:

```bash
pebble pull origin main
```

## Example Projects

### Overview

Example projects are designed to demonstrate how to use the core systems in practice. They include step-by-step instructions and explanations of key concepts, making it easier for beginners to understand and contribute to the project.

### Available Example Projects

1. **Basic Repository Setup**: Demonstrates how to set up a basic Pebble repository and make your first commit.
2. **Binary Asset Handling**: Demonstrates how to handle binary assets using the ROCK Binary System.
3. **Conflict Resolution**: Demonstrates how to resolve conflicts using the Combine Operation.
4. **Remote Sync**: Demonstrates how to sync changes with a remote repository using the Remote Sync Protocol.

### Usage Examples

1. Basic Repository Setup:

```bash
cd examples/basic_repository_setup
pebble init
pebble commit "Initial commit"
```

2. Binary Asset Handling:

```bash
cd examples/binary_asset_handling
pebble init
pebble add binary_file.bin
pebble commit "Add binary file"
```

3. Conflict Resolution:

```bash
cd examples/conflict_resolution
pebble init
pebble commit "Initial commit"
# Make changes to create a conflict
pebble combine
```

4. Remote Sync:

```bash
cd examples/remote_sync
pebble init
pebble commit "Initial commit"
pebble push origin main
```

## Tutorials

### Overview

Tutorials provide hands-on exercises and examples to help new programmers get started with the project. They cover a range of topics, from setting up the project to making your first contribution.

### Available Tutorials

1. **Beginner Tutorial**: A step-by-step guide to setting up the project, understanding the core concepts, and making your first contribution.
2. **Advanced Tutorial**: A more in-depth guide to the core systems and advanced features of the project.
3. **Contributing Guide**: A guide to contributing to the project, including setting up your development environment, making changes, and submitting pull requests.

### Usage Examples

1. Beginner Tutorial:

```bash
cd tutorials/beginner_tutorial
# Follow the step-by-step instructions
```

2. Advanced Tutorial:

```bash
cd tutorials/advanced_tutorial
# Follow the step-by-step instructions
```

3. Contributing Guide:

```bash
cd tutorials/contributing_guide
# Follow the step-by-step instructions
```

## Conclusion

The beginner adapters for Pebble + ROCK are designed to simplify the experience and make it easier for new programmers to get started. They provide a more intuitive interface, example projects, and tutorials to help beginners understand and contribute to the project.

For more information, please refer to the [documentation](docs/checklists/phase_1_core_vcs_checklist.md) and the [README](README.md).
