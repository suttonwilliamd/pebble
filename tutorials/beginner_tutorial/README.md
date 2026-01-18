# Beginner Tutorial

This tutorial provides a step-by-step guide to setting up the project, understanding the core concepts, and making your first contribution.

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

4. Create a new commit:

```bash
pebble commit "Initial commit"
```

5. View the commit history:

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