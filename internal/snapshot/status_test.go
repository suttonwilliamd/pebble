package snapshot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func writeFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func testRepo(t *testing.T) *Repository {
	t.Helper()
	repo, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func storeCommit(t *testing.T, repo *Repository, tree *Object) string {
	t.Helper()
	if err := repo.StoreObject(tree); err != nil {
		t.Fatal(err)
	}
	commit := CreateCommit(tree.Hash, "", "Tester", "test@example.invalid", "fixture")
	if err := repo.StoreObject(commit); err != nil {
		t.Fatal(err)
	}
	ref, err := repo.GetHead()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetRef(ref, commit.Hash); err != nil {
		t.Fatal(err)
	}
	return commit.Hash
}

func commitFiles(t *testing.T, repo *Repository) string {
	t.Helper()
	index, err := WorkingIndex(repo)
	if err != nil {
		t.Fatal(err)
	}
	var entries []TreeEntry
	for _, entry := range index.Entries {
		if entry.Hash == "" {
			continue
		}
		obj, err := CreateBlob(filepath.Join(repo.RootPath(), filepath.FromSlash(entry.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.StoreObject(obj); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, TreeEntry{Name: entry.Path, Mode: entry.Mode, Hash: entry.Hash, Type: ObjectTypeBlob})
	}
	if err := repo.SaveIndex(index); err != nil {
		t.Fatal(err)
	}
	return storeCommit(t, repo, CreateTree(entries))
}

func requireChanges(t *testing.T, repo *Repository, want []Change) *Status {
	t.Helper()
	status, err := WorkingTreeStatus(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(status.Changes, want) {
		t.Fatalf("changes = %#v, want %#v", status.Changes, want)
	}
	return status
}

func TestStatusUnbornAndClean(t *testing.T) {
	repo := testRepo(t)
	status := requireChanges(t, repo, nil)
	if !status.Unborn || status.Branch != "main" {
		t.Fatalf("status = %#v", status)
	}
	writeFile(t, repo.RootPath(), "asset.bin", []byte{0, 255, 1})
	requireChanges(t, repo, []Change{{"asset.bin", "added"}})
	commitFiles(t, repo)
	if requireChanges(t, repo, nil).Unborn {
		t.Fatal("committed repository reported unborn")
	}
}

func TestStatusAddedModifiedDeletedAndMtime(t *testing.T) {
	repo := testRepo(t)
	writeFile(t, repo.RootPath(), "asset.bin", []byte{0, 255, 1})
	writeFile(t, repo.RootPath(), "deleted.txt", []byte("bye"))
	writeFile(t, repo.RootPath(), "same.txt", []byte("same"))
	commitFiles(t, repo)
	info, err := os.Stat(filepath.Join(repo.RootPath(), "asset.bin"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo.RootPath(), "asset.bin", []byte{0, 255, 2}) // Same size, preserved mtime, changed bytes.
	if err := os.Chtimes(filepath.Join(repo.RootPath(), "asset.bin"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo.RootPath(), "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo.RootPath(), "nested/new.txt", []byte("new"))
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(repo.RootPath(), "same.txt"), future, future); err != nil {
		t.Fatal(err)
	}
	requireChanges(t, repo, []Change{{"asset.bin", "modified"}, {"deleted.txt", "deleted"}, {"nested/new.txt", "added"}})
}

func TestStatusUsesHeadNotSavedIndex(t *testing.T) {
	repo := testRepo(t)
	writeFile(t, repo.RootPath(), "file.txt", []byte("main"))
	main := commitFiles(t, repo)
	if err := repo.SetRef("heads/feature/nested", main); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetHead("heads/feature/nested"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo.RootPath(), "file.txt", []byte("feature"))
	commitFiles(t, repo) // Saved index now describes the feature branch.
	if err := repo.SetHead("heads/main"); err != nil {
		t.Fatal(err)
	}
	status := requireChanges(t, repo, []Change{{"file.txt", "modified"}})
	if status.Branch != "main" {
		t.Fatal(status.Branch)
	}
	if err := repo.SetHead("heads/feature/nested"); err != nil {
		t.Fatal(err)
	}
	if requireChanges(t, repo, nil).Branch != "feature/nested" {
		t.Fatal("nested branch not reported")
	}
	if err := os.Remove(filepath.Join(repo.RootPath(), ".pebble", "index")); err != nil {
		t.Fatal(err)
	}
	requireChanges(t, repo, nil) // Status is independent of the saved index entirely.
}

func TestIgnoredTrackedFilesRemainVisibleAndCommittable(t *testing.T) {
	repo := testRepo(t)
	writeFile(t, repo.RootPath(), "build/tracked.bin", []byte{1})
	writeFile(t, repo.RootPath(), "build/deleted.bin", []byte{2})
	commitFiles(t, repo)
	writeFile(t, repo.RootPath(), ".pebbleignore", []byte("# shared rules\r\n\r\nbuild/\r\n*.log\r\n"))
	writeFile(t, repo.RootPath(), "build/tracked.bin", []byte{3})
	writeFile(t, repo.RootPath(), "build/new.bin", []byte{4})
	writeFile(t, repo.RootPath(), "debug.log", []byte("ignored"))
	if err := os.Remove(filepath.Join(repo.RootPath(), "build/deleted.bin")); err != nil {
		t.Fatal(err)
	}
	requireChanges(t, repo, []Change{{".pebbleignore", "added"}, {"build/deleted.bin", "deleted"}, {"build/tracked.bin", "modified"}})
	commitFiles(t, repo)
	requireChanges(t, repo, nil)
	_, _, entries, err := headEntries(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries["build/tracked.bin"].Hash == "" {
		t.Fatalf("committed entries = %#v", entries)
	}
}

func TestStatusDoesNotInitializeOrRepair(t *testing.T) {
	root := t.TempDir()
	if _, err := OpenRepository(root); err == nil {
		t.Fatal("opened uninitialized directory")
	}
	if _, err := os.Stat(filepath.Join(root, ".pebble")); !os.IsNotExist(err) {
		t.Fatal("read created metadata")
	}
	repo := testRepo(t)
	if err := os.Remove(repo.ObjectsPath()); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenRepository(repo.RootPath())
	if err != nil {
		t.Fatal(err)
	}
	requireChanges(t, opened, nil)
	if _, err := os.Stat(repo.ObjectsPath()); !os.IsNotExist(err) {
		t.Fatal("status repaired objects directory")
	}
}

func TestStatusRejectsBrokenHeadAndObjects(t *testing.T) {
	for _, name := range []string{"missing HEAD", "bad HEAD", "missing ref", "invalid hash", "missing commit", "corrupt commit", "wrong commit type", "missing tree", "corrupt tree"} {
		t.Run(name, func(t *testing.T) {
			repo := testRepo(t)
			switch name {
			case "missing HEAD":
				if err := os.Remove(repo.headPath); err != nil {
					t.Fatal(err)
				}
			case "bad HEAD":
				writeFile(t, repo.RootPath(), ".pebble/HEAD", []byte("nonsense"))
			case "missing ref":
				if err := os.Remove(filepath.Join(repo.refsPath, "heads", "main")); err != nil {
					t.Fatal(err)
				}
			case "invalid hash":
				if err := repo.SetRef("heads/main", "z"); err != nil {
					t.Fatal(err)
				}
			case "missing commit":
				if err := repo.SetRef("heads/main", ComputeHash([]byte("missing"))); err != nil {
					t.Fatal(err)
				}
			case "corrupt commit", "wrong commit type":
				hash := commitFiles(t, repo)
				obj, err := repo.GetObject(hash)
				if err != nil {
					t.Fatal(err)
				}
				if name == "corrupt commit" {
					obj.Content = []byte("corrupt")
				} else {
					obj.Type = ObjectTypeBlob
				}
				data, err := json.Marshal(obj)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, repo.RootPath(), ".pebble/objects/"+hash[:2]+"/"+hash[2:], data)
			case "missing tree":
				commit := CreateCommit(ComputeHash([]byte("missing")), "", "", "", "fixture")
				if err := repo.StoreObject(commit); err != nil {
					t.Fatal(err)
				}
				if err := repo.SetRef("heads/main", commit.Hash); err != nil {
					t.Fatal(err)
				}
			case "corrupt tree":
				hash := commitFiles(t, repo)
				commit, err := repo.GetCommit(hash)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, repo.RootPath(), ".pebble/objects/"+commit.Tree[:2]+"/"+commit.Tree[2:], []byte("invalid json"))
			}
			if _, err := WorkingTreeStatus(repo); err == nil {
				t.Fatal("broken repository reported clean or unborn")
			}
		})
	}
}

func TestStatusRejectsUnsafeAndDuplicateTreePaths(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:/absolute", ".git/config", ".pebble/HEAD", "nested/../escape", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			repo := testRepo(t)
			entry := TreeEntry{Name: name, Type: ObjectTypeBlob, Hash: ComputeHash(nil), Mode: "-rw-r--r--"}
			entries := []TreeEntry{entry}
			if name == "duplicate" {
				entries = append(entries, entry)
			}
			storeCommit(t, repo, CreateTree(entries))
			if _, err := WorkingTreeStatus(repo); err == nil {
				t.Fatal("accepted unsafe tree")
			}
		})
	}
}

func TestStatusLegacyWindowsTreePaths(t *testing.T) {
	repo := testRepo(t)
	writeFile(t, repo.RootPath(), "nested/file.txt", []byte("portable"))
	index, err := WorkingIndex(repo)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	for _, entry := range index.Entries {
		if entry.Hash != "" {
			mode = entry.Mode
		}
	}
	storeCommit(t, repo, CreateTree([]TreeEntry{{Name: "nested\\file.txt", Mode: mode, Hash: ComputeHash([]byte("portable")), Type: ObjectTypeBlob}}))
	requireChanges(t, repo, nil)
}

func TestStatusExecutableAndSymlinkBehavior(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable bits and symlinks require Linux")
	}
	repo := testRepo(t)
	writeFile(t, repo.RootPath(), "script", []byte("text"))
	commitFiles(t, repo)
	if err := os.Chmod(filepath.Join(repo.RootPath(), "script"), 0755); err != nil {
		t.Fatal(err)
	}
	requireChanges(t, repo, []Change{{"script", "modified"}})
	if err := os.Remove(filepath.Join(repo.RootPath(), "script")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("do not follow"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repo.RootPath(), "script")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repo.RootPath(), "untracked-link")); err != nil {
		t.Fatal(err)
	}
	requireChanges(t, repo, []Change{{"script", "deleted"}})
}

func TestRefEmptyAndLineEndings(t *testing.T) {
	repo := testRepo(t)
	for _, value := range []string{"", "\n", "\r\n", "abcdef", "abcdef\r\n"} {
		writeFile(t, repo.RootPath(), ".pebble/refs/heads/main", []byte(value))
		got, err := repo.GetRef("heads/main")
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if len(value) > 2 {
			want = "abcdef"
		}
		if got != want {
			t.Fatalf("GetRef(%q) = %q", value, got)
		}
	}
}

func TestPortableFilePathsFailBeforeCommit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows already prohibits these filenames")
	}
	for _, name := range []string{"name:with-colon", "name\\with-backslash"} {
		t.Run(name, func(t *testing.T) {
			repo := testRepo(t)
			writeFile(t, repo.RootPath(), name, []byte("data"))
			if _, err := WorkingIndex(repo); err == nil {
				t.Fatal("accepted ambiguous portable filename")
			}
			if _, err := WorkingTreeStatus(repo); err == nil {
				t.Fatal("accepted ambiguous portable filename")
			}
		})
	}
}

func TestStatusIgnoredTrackedFileIsNotAddedAfterDeletion(t *testing.T) {
	repo := testRepo(t)
	writeFile(t, repo.RootPath(), "cache/tracked.txt", []byte("data"))
	commitFiles(t, repo)
	writeFile(t, repo.RootPath(), ".pebbleignore", []byte("cache/\n"))
	if err := os.Remove(filepath.Join(repo.RootPath(), "cache", "tracked.txt")); err != nil {
		t.Fatal(err)
	}
	commitFiles(t, repo)
	writeFile(t, repo.RootPath(), "cache/tracked.txt", []byte("now untracked and ignored"))
	requireChanges(t, repo, nil)
}
