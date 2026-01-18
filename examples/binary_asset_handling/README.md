# Binary Asset Handling Example

This example demonstrates how to handle binary assets using the ROCK Binary System.

## Steps

1. Initialize a new Pebble repository:

```bash
pebble init
```

2. Add a binary file to the repository:

```bash
pebble add binary_file.bin
```

3. Create a new commit:

```bash
pebble commit "Add binary file"
```

4. View the commit history:

```bash
pebble log
```

## Expected Output

After running the above commands, you should see the following output:

```bash
Initialized empty Pebble repository in .pebble
[main (root-commit) abc1234] Add binary file
1 file changed, 1 insertion(+)
commit abc1234
Author: Your Name <your.email@example.com>
Date:   Mon Jan 18 00:00:00 2026 +0000

    Add binary file