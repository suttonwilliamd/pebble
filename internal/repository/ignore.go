package repository

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/ignore"
)

func (r *Repository) ignoreMatcher() *ignore.Matcher {
	return ignore.New(func(name string) ([]byte, error) {
		var data []byte
		present, err := r.readFileWithPresence(name, func(f *os.File) error {
			var err error
			data, err = io.ReadAll(f)
			return err
		})
		if !present && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return data, err
	})
}

type IgnoreResult struct {
	Path    string
	Tracked bool
	ignore.Result
}

// CheckIgnore uses the index to identify tracked paths, including staged adds
// and deleted working files. It never follows a link to load descendant rules.
func (r *Repository) CheckIgnore(names []string) (_ []IgnoreResult, err error) {
	unlock, err := r.lock()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	_, entries, err := r.index()
	if err != nil {
		return nil, err
	}
	tracked := make(map[string]bool, len(entries))
	for _, e := range entries {
		tracked[e.Path] = true
	}
	matcher := r.ignoreMatcher()
	results := make([]IgnoreResult, 0, len(names))
	for _, input := range names {
		name := strings.TrimSuffix(input, "/")
		// Internal paths are explainable but must never enter safePath or storage.
		protected := false
		for _, part := range strings.Split(name, "/") {
			if strings.EqualFold(part, ".pebble") || strings.EqualFold(part, ".git") {
				protected = true
			}
		}
		result := IgnoreResult{Path: input, Tracked: tracked[name]}
		if result.Tracked {
			results = append(results, result)
			continue
		}
		directory := strings.HasSuffix(input, "/")
		if !protected {
			p, err := r.safePath(name)
			if err != nil {
				return nil, err
			}
			info, err := os.Lstat(p)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			if err == nil {
				directory = info.IsDir() && info.Mode()&os.ModeSymlink == 0
			}
		}
		result.Result, err = matcher.Check(name, directory)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}
