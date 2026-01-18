# Contributing Guide

This guide provides instructions for contributing to the project, including setting up your development environment, making changes, and submitting pull requests.

## Steps

1. Fork the repository on GitHub.

2. Clone your fork to your local machine:

```bash
git clone https://github.com/yourusername/pebble.git
cd pebble
```

3. Set up the project:

```bash
go build -o pebble ./cmd/pebble
```

4. Create a new branch for your feature or bug fix:

```bash
git checkout -b feature/your-feature-name
```

5. Make your changes and commit them:

```bash
git commit -m "Add your commit message"
```

6. Push your changes to your fork:

```bash
git push origin feature/your-feature-name
```

7. Open a pull request on GitHub.

## Expected Output

After running the above commands, you should see the following output:

```bash
Switched to a new branch 'feature/your-feature-name'
[feature/your-feature-name abc1234] Add your commit message
1 file changed, 1 insertion(+)
Enumerating objects: 5, done.
Counting objects: 100% (5/5), done.
Delta compression using up to 4 threads.
Compressing objects: 100% (3/3), done.
Writing objects: 100% (3/3), 320 bytes | 320.00 KiB/s, done.
Total 3 (delta 2), reused 0 (delta 0)
To https://github.com/yourusername/pebble.git
 * [new branch]      feature/your-feature-name -> feature/your-feature-name