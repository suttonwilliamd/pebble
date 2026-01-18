# Remote Sync Example

This example demonstrates how to sync changes with a remote repository using the Remote Sync Protocol.

## Steps

1. Initialize a new Pebble repository:

```bash
pebble init
```

2. Create a new commit:

```bash
pebble commit "Initial commit"
```

3. Push changes to a remote repository:

```bash
pebble push origin main
```

4. Pull changes from a remote repository:

```bash
pebble pull origin main
```

## Expected Output

After running the above commands, you should see the following output:

```bash
Initialized empty Pebble repository in .pebble
[main (root-commit) abc1234] Initial commit
1 file changed, 1 insertion(+)
Counting objects: 3, done.
Delta compression using up to 4 threads.
Compressing objects: 100% (2/2), done.
Writing objects: 100% (3/3), 256 bytes | 256.00 KiB/s, done.
Total 3 (delta 0), reused 0 (delta 0)
To https://github.com/yourusername/pebble.git
 * [new branch]      main -> main
Branch 'main' set up to track remote branch 'main' from 'origin'.
Already up to date.