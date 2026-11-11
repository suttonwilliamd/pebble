//go:build !windows

package storev2

import (
	"errors"
	"os"
	"path/filepath"
)

func checkPlatform(root string) error { return syncDirectory(root) }

func syncDirectory(path string) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return f.Sync()
}

func publishFile(source, target string, fault func(string, string) error) (bool, error) {
	if err := os.Rename(source, target); err != nil {
		return false, err
	}
	if fault != nil {
		if err := fault("namespace-sync", target); err != nil {
			return true, err
		}
	}
	return true, syncDirectory(filepath.Dir(target))
}

func publishDirectory(source, target string) error {
	if err := syncDirectory(source); err != nil {
		return err
	}
	_, err := publishFile(source, target, nil)
	return err
}

func makeDurableDirectory(path string) error {
	temporary, err := os.MkdirTemp(filepath.Dir(path), tempPrefix)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = os.ErrExist
		}
		return err
	}
	return publishDirectory(temporary, path)
}
