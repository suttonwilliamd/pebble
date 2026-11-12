package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func publicSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if name == ".pebble" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		identity, err := filesystemIdentity(path)
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%x:%s:", uint32(info.Mode()), identity)
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := readLink(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf("link:%t:%s", link.directory, link.target)
		} else if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += "file:" + string(data)
		}
		out[name] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func assertPublicSnapshot(t *testing.T, root string, want map[string]string) {
	t.Helper()
	got := publicSnapshot(t, root)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PUBLIC namespace changed after failed operation:\ngot: %#v\nwant: %#v", got, want)
	}
}
