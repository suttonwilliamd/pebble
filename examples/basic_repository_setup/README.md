# Basic Repository Setup Example

This example demonstrates how to set up a basic Pebble repository and make your first commit.

## Steps

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

## Expected Output

After running the above commands, you should see the following output:

```bash
Initialized empty Pebble repository in .pebble
[main (root-commit) abc1234] Initial commit
1 file changed, 1 insertion(+)
commit abc1234
Author: Your Name <your.email@example.com>
Date:   Mon Jan 18 00:00:00 2026 +0000

    Initial commit