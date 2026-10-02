package snapshot

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// LoadIgnorePatterns reads the repository's root .pebbleignore. Supported rules
// are comments, blank lines, basename/path globs, root anchors and directory rules.
// Negation and recursive ** globs are rejected rather than silently misinterpreted.
func LoadIgnorePatterns(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, ".pebbleignore"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read .pebbleignore: %w", err)
	}
	var patterns []string
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := validateIgnorePattern(line); err != nil {
			return nil, fmt.Errorf(".pebbleignore line %d: %w", i+1, err)
		}
		patterns = append(patterns, line)
	}
	return patterns, nil
}

func validateIgnorePattern(pattern string) error {
	if strings.HasPrefix(pattern, "!") || strings.Contains(pattern, "**") {
		return fmt.Errorf("unsupported ignore pattern %q (negation and ** are not supported)", pattern)
	}
	pattern = strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/")
	if pattern == "" {
		return fmt.Errorf("empty ignore pattern")
	}
	if _, err := path.Match(pattern, ""); err != nil {
		return fmt.Errorf("invalid ignore pattern %q: %w", pattern, err)
	}
	return nil
}

func metadataPath(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == ".pebble" || part == ".git" || (runtime.GOOS == "windows" && (strings.EqualFold(part, ".pebble") || strings.EqualFold(part, ".git"))) {
			return true
		}
	}
	return false
}

// matchesIgnore also checks ancestors. This matters when an ignored directory
// must still be traversed to inspect a previously tracked file within it.
func matchesIgnore(name string, isDir bool, patterns []string) bool {
	parts := strings.Split(name, "/")
	for _, pattern := range patterns {
		dirOnly := strings.HasSuffix(pattern, "/")
		anchored := strings.HasPrefix(pattern, "/")
		pattern = strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/")
		for i := range parts {
			candidateDir := i < len(parts)-1 || isDir
			if dirOnly && !candidateDir {
				continue
			}
			candidate := parts[i]
			if anchored || strings.Contains(pattern, "/") {
				candidate = strings.Join(parts[:i+1], "/")
			}
			matched, _ := path.Match(pattern, candidate)
			if matched {
				return true
			}
		}
	}
	return false
}
