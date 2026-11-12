package repository

import (
	"fmt"
	"os"
	"path/filepath"
)

// Resolve only the selected existing root (including OS aliases such as /var),
// never stored tree components. Anchored operations subsequently use its real
// path. A symlink leaf is not accepted as a repository-directory object.
func workingRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("repository path is not an ordinary directory: %s", absolute)
	}
	return filepath.EvalSymlinks(absolute)
}
