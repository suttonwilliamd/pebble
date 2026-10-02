package snapshot

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIgnoreRules(t *testing.T) {
	patterns := []string{"*.log", "build/", "/root.txt", "assets/*.tmp", "cache", "file?.bak", "[ab].tmp"}
	cases := []struct {
		name         string
		dir, ignored bool
	}{
		{"debug.log", false, true}, {"nested/debug.log", false, true},
		{"build", true, true}, {"build", false, false}, {"build/file.bin", false, true},
		{"nested/build/file.bin", false, true}, {"build-notes.txt", false, false},
		{"root.txt", false, true}, {"nested/root.txt", false, false},
		{"assets/foo.tmp", false, true}, {"assets/sub/foo.tmp", false, false},
		{"cache", false, true}, {"nested/cache", true, true},
		{"file1.bak", false, true}, {"a.tmp", false, true}, {"c.tmp", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchesIgnore(c.name, c.dir, patterns); got != c.ignored {
				t.Fatalf("ignored = %v, want %v", got, c.ignored)
			}
		})
	}
}

func TestIgnoreLoaderErrors(t *testing.T) {
	root := t.TempDir()
	patterns, err := LoadIgnorePatterns(root)
	if err != nil || len(patterns) != 0 {
		t.Fatalf("missing ignore: %v %v", patterns, err)
	}
	writeFile(t, root, ".pebbleignore", []byte("# comment\r\n \r\n *.log \r\nbuild/\r\n"))
	patterns, err = LoadIgnorePatterns(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(patterns, []string{"*.log", "build/"}) {
		t.Fatal(patterns)
	}
	for _, invalid := range []string{"!secret", "**/secret", "[", "/"} {
		writeFile(t, root, ".pebbleignore", []byte(invalid))
		if _, err := LoadIgnorePatterns(root); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	if err := os.Remove(filepath.Join(root, ".pebbleignore")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".pebbleignore"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIgnorePatterns(root); err == nil {
		t.Fatal("ignore read failure ignored")
	}
}

func TestIndexAlwaysExcludesMetadata(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".git/config", ".pebble/HEAD", "nested/.git/config", "nested/.pebble/HEAD", "visible/file.txt"} {
		writeFile(t, root, name, []byte("data"))
	}
	index, err := NewFileIndexGenerator(root, nil).GenerateIndex()
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range index.Entries {
		if entry.Hash != "" {
			files = append(files, entry.Path)
		}
	}
	if !reflect.DeepEqual(files, []string{"visible/file.txt"}) {
		t.Fatal(files)
	}
}

func TestIndexDirectoryRuleSkipsDirectoryItself(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "build/artifact.bin", []byte{0})
	writeFile(t, root, "visible.txt", []byte("ok"))
	index, err := NewFileIndexGenerator(root, []string{"build/"}).GenerateIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Entries) != 1 || index.Entries[0].Path != "visible.txt" {
		t.Fatalf("index = %#v", index.Entries)
	}
}
