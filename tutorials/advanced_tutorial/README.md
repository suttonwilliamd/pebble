# Advanced Tutorial

This tutorial provides a more in-depth guide to the core systems and advanced features of the project.

## Steps

1. Clone the repository:

```bash
git clone https://github.com/suttonwilliamd/pebble.git
cd pebble
```

2. Build the project:

```bash
go build -o pebble ./cmd/pebble
```

3. Initialize a new Pebble repository:

```bash
pebble init
```

4. Add a binary file to the repository:

```bash
pebble add binary_file.bin
```

5. Create a new commit:

```bash
pebble commit "Add binary file"
```

6. View the commit history:

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