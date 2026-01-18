# Conflict Resolution Example

This example demonstrates how to resolve conflicts using the Combine Operation.

## Steps

1. Initialize a new Pebble repository:

```bash
pebble init
```

2. Create a new commit:

```bash
pebble commit "Initial commit"
```

3. Make changes to create a conflict:

```bash
echo "Conflict line" > conflict.txt
pebble add conflict.txt
pebble commit "Add conflict.txt"
```

4. Use the Combine Operation to resolve the conflict:

```bash
pebble combine
```

5. Commit the resolved changes:

```bash
pebble commit "Resolve conflict"
```

## Expected Output

After running the above commands, you should see the following output:

```bash
Initialized empty Pebble repository in .pebble
[main (root-commit) abc1234] Initial commit
1 file changed, 1 insertion(+)
[main abc1234] Add conflict.txt
1 file changed, 1 insertion(+)
Conflict detected in conflict.txt
Resolved conflict in conflict.txt
[main abc1234] Resolve conflict
1 file changed, 1 insertion(+)