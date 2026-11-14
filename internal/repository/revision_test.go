package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func revisionFixture(t *testing.T) (*Repository, storev2.ID, storev2.ID) {
	t.Helper()
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "file", []byte("old\nold\nlast\n"))
	write(t, r.root, "untouched", []byte("safe"))
	first := committed(t, r, "first")
	if err = r.CreateBranch("first"); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "file", []byte("last\nold\nold"))
	second := committed(t, r, "second")
	return r, first, second
}

func metadataSnapshot(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(filepath.Join(root, ".pebble"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		if d.IsDir() {
			out[name] = [32]byte{}
			return nil
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		out[name] = sha256.Sum256(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestRevisionResolutionAndOrderedHistory(t *testing.T) {
	r, first, second := revisionFixture(t)
	ctx := context.Background()
	for spelling, want := range map[string]storev2.ID{"HEAD": second, "first": first, "heads/first": first, first.String(): first, "HEAD~1": first, "HEAD^1": first, "HEAD^0": second, "HEAD~0": second} {
		got, err := r.ResolveRevision(ctx, spelling)
		if err != nil || got != want {
			t.Fatalf("%s: %s %v", spelling, got, err)
		}
	}
	if err := r.store.UpdateRef(ctx, "tags/first", nil, first); err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{"", "first", "HEAD~2", "HEAD^2", "HEAD^-1", "HEAD~999999999999999999999", "HEAD..first", "file", "pv2:commit:sha256:" + strings.Repeat("0", 64)} {
		if _, err := r.ResolveRevision(ctx, spelling); err == nil {
			t.Fatalf("invalid/missing/ambiguous revision accepted: %q", spelling)
		}
	}
	c, err := r.GetCommit(second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ResolveRevision(ctx, c.Tree.String()); err == nil {
		t.Fatal("tree accepted as commit revision")
	}
	if err = r.store.UpdateRef(ctx, "tags/tree", nil, c.Tree); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ResolveRevision(ctx, "tags/tree"); err == nil {
		t.Fatal("wrong-kind ref accepted")
	}
	c.Parents = []storev2.ID{second, first}
	c.Message = "merge graph fixture"
	merge, err := r.store.CreateCommit(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.store.UpdateRef(ctx, "heads/main", &second, merge); err != nil {
		t.Fatal(err)
	}
	var ids []storev2.ID
	if err = r.LogRevision(ctx, "HEAD", func(id storev2.ID, _ storev2.CommitData) error { ids = append(ids, id); return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []storev2.ID{merge, second, first}) {
		t.Fatalf("ordered unique parents: %v", ids)
	}
	got, err := r.ResolveRevision(ctx, "HEAD^2")
	if err != nil || got != first {
		t.Fatal("second ordered parent", got, err)
	}
}

func TestRevisionInspectionReadOnlyDuringRecovery(t *testing.T) {
	r, first, _ := revisionFixture(t)
	write(t, r.root, "file", []byte("staged\n"))
	if err := r.Stage([]string{"file"}); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "file", []byte("dirty before intent\n"))
	injected := errors.New("stop after intent")
	r.fault = func(stage, path string) error {
		if stage == "journal-published" {
			return injected
		}
		return nil
	}
	if err := r.RestoreFromContext(context.Background(), first.String(), "file"); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	r.fault = nil
	r, err := Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	before := metadataSnapshot(t, r.root)
	var raw, patch bytes.Buffer
	if err = r.ShowFile(context.Background(), "HEAD~1", "file", &raw); err != nil || raw.String() != "old\nold\nlast\n" {
		t.Fatal(raw.String(), err)
	}
	if err = r.Diff(context.Background(), DiffOptions{Revisions: []string{"heads/first", "HEAD"}}, &patch); err != nil || !strings.Contains(patch.String(), "+last\n+old\n+old\n\\ No newline") {
		t.Fatal(patch.String(), err)
	}
	if err = r.Diff(context.Background(), DiffOptions{Paths: []string{"file"}}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err = r.Diff(context.Background(), DiffOptions{Staged: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	var history []storev2.ID
	if err = r.LogRevision(context.Background(), "HEAD~1", func(id storev2.ID, _ storev2.CommitData) error { history = append(history, id); return nil }); err != nil || !reflect.DeepEqual(history, []storev2.ID{first}) {
		t.Fatal(history, err)
	}
	if after := metadataSnapshot(t, r.root); !reflect.DeepEqual(before, after) {
		t.Fatal("inspection changed repository metadata or pending recovery")
	}
	if err = r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(r.root, "file"))
	if err != nil || string(got) != "old\nold\nlast\n" {
		t.Fatal(string(got), err)
	}
}

func TestRevisionInspectionCancellationAndOutputFailure(t *testing.T) {
	r, first, _ := revisionFixture(t)
	before := metadataSnapshot(t, r.root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range []error{
		r.ShowFile(ctx, "HEAD", "file", io.Discard), r.Diff(ctx, DiffOptions{}, io.Discard), r.LogRevision(ctx, "HEAD", func(storev2.ID, storev2.CommitData) error { return nil }), r.RestoreFromContext(ctx, first.String(), "file"),
	} {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation hidden: %v", err)
		}
	}
	for _, err := range []error{r.ShowFile(context.Background(), "HEAD", "file", revisionClosedOutput{}), r.Diff(context.Background(), DiffOptions{Revisions: []string{"first", "HEAD"}}, revisionClosedOutput{}), r.DiffCommit(context.Background(), "first", revisionClosedOutput{})} {
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("output failure hidden: %v", err)
		}
	}
	if after := metadataSnapshot(t, r.root); !reflect.DeepEqual(before, after) {
		t.Fatal("failed inspection/cancellation changed metadata")
	}
}

type revisionClosedOutput struct{}

func (revisionClosedOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRevisionRestoreProcessHelper(t *testing.T) {
	root := os.Getenv("PEBBLE_TEST_REVISION_ROOT")
	if root == "" {
		return
	}
	r, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r.fault = func(stage, path string) error {
		if stage == os.Getenv("PEBBLE_TEST_REVISION_STAGE") {
			os.Exit(73)
		}
		return nil
	}
	err = r.RestoreFromContext(context.Background(), os.Getenv("PEBBLE_TEST_REVISION_SOURCE"), "file")
	t.Fatal("process boundary not reached", err)
}
func TestRevisionRestoreInterruptedRecovery(t *testing.T) {
	for _, stage := range []string{"stage-sync", "journal-published", "retain-published", "install-published", "head-published"} {
		t.Run(stage, func(t *testing.T) {
			r, first, head := revisionFixture(t)
			write(t, r.root, "file", []byte("staged\n"))
			if err := r.Stage([]string{"file"}); err != nil {
				t.Fatal(err)
			}
			index, err := os.ReadFile(filepath.Join(r.root, ".pebble", "index"))
			if err != nil {
				t.Fatal(err)
			}
			write(t, r.root, "file", []byte("discarded dirty preimage\n"))
			write(t, r.root, "untracked", []byte("untracked survives"))
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			child := exec.Command(exe, "-test.run=^TestRevisionRestoreProcessHelper$", "-test.timeout=30s")
			child.Env = append(os.Environ(), "PEBBLE_TEST_REVISION_ROOT="+r.root, "PEBBLE_TEST_REVISION_SOURCE="+first.String(), "PEBBLE_TEST_REVISION_STAGE="+stage)
			out, err := child.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("actual interruption: %v\n%s", err, out)
			}
			r, err = Open(r.root)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := "old\nold\nlast\n"
			if stage == "stage-sync" {
				want = "discarded dirty preimage\n"
			}
			got, err := os.ReadFile(filepath.Join(r.root, "file"))
			if err != nil || string(got) != want {
				t.Fatalf("reopened restored bytes %q want %q: %v", got, want, err)
			}
			actual, err := r.CurrentID()
			if err != nil || actual != head {
				t.Fatal("history moved", actual, err)
			}
			current, err := os.ReadFile(filepath.Join(r.root, ".pebble", "index"))
			if err != nil || !bytes.Equal(index, current) {
				t.Fatal("index changed", err)
			}
			got, err = os.ReadFile(filepath.Join(r.root, "untracked"))
			if err != nil || string(got) != "untracked survives" {
				t.Fatal("untracked lost", err)
			}
		})
	}
}
func TestRevisionRestoreReconcilesLaterEdit(t *testing.T) {
	r, first, head := revisionFixture(t)
	write(t, r.root, "file", []byte("dirty original"))
	injected := errors.New("interrupted after installing historical bytes")
	r.fault = func(stage, path string) error {
		if stage == "install-published" {
			return injected
		}
		return nil
	}
	if err := r.RestoreFromContext(context.Background(), first.String(), "file"); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	r.fault = nil
	write(t, r.root, "file", []byte("later external edit"))
	r, err := Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	var aborted *RecoveryAbortedError
	if err = r.Recover(context.Background()); !errors.As(err, &aborted) {
		t.Fatalf("not a safe abort: %v", err)
	}
	if err = r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(r.root, "file"))
	if err != nil || string(got) != "later external edit" {
		t.Fatal("external edit overwritten", err)
	}
	receipts, err := r.RecoveryReceipts()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, receipt := range receipts {
		if receipt.RestoreSource == nil || *receipt.RestoreSource != first {
			t.Fatal("abort receipt lost exact historical source")
		}
		for _, version := range receipt.Versions {
			data, e := os.ReadFile(version.Location)
			if e == nil && string(data) == "dirty original" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("dirty preimage not discoverable in preserved versions")
	}
	actual, err := r.CurrentID()
	if err != nil || actual != head {
		t.Fatal("abort moved history", err)
	}
}
func TestRevisionRestoreStagedAdditionAndTransitions(t *testing.T) {
	r, first, _ := revisionFixture(t)
	write(t, r.root, "staged-only", []byte("staged addition"))
	if err := r.Stage([]string{"staged-only"}); err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(r.root, ".pebble", "index"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "staged-only", []byte("later working addition"))
	if err = r.RestoreFromContext(context.Background(), first.String(), "staged-only"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(filepath.Join(r.root, "staged-only")); !os.IsNotExist(err) {
		t.Fatal("source deletion did not remove selected staged addition", err)
	}
	after, err := os.ReadFile(filepath.Join(r.root, ".pebble", "index"))
	if err != nil || !bytes.Equal(index, after) {
		t.Fatal("deletion unstaged addition", err)
	}
	// Literal source-only paths never authorize replacing unrelated live bytes.
	write(t, r.root, "historic", []byte("old source"))
	old := committed(t, r, "has historic")
	if err = os.Remove(filepath.Join(r.root, "historic")); err != nil {
		t.Fatal(err)
	}
	committed(t, r, "removed historic")
	write(t, r.root, "historic", []byte("precious untracked"))
	before := metadataSnapshot(t, r.root)
	if err = r.RestoreFromContext(context.Background(), old.String(), "historic"); err == nil {
		t.Fatal("untracked collision accepted")
	}
	if after := metadataSnapshot(t, r.root); !reflect.DeepEqual(before, after) {
		t.Fatal("refused restore changed metadata")
	}
	got, err := os.ReadFile(filepath.Join(r.root, "historic"))
	if err != nil || string(got) != "precious untracked" {
		t.Fatal("collision lost bytes", err)
	}
}

func TestRevisionBinaryRegionsAndLengths(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := []byte{0, 1, 2, 3, 4, 5, 6, 7}
	write(t, r.root, "binary", original)
	first := committed(t, r, "original")
	for _, tc := range []struct {
		name    string
		data    []byte
		regions string
	}{
		{"prefix-middle-multiple", []byte{255, 1, 2, 9, 8, 5, 0, 7}, "region old offset=0 length=1 new offset=0 length=1\nregion old offset=3 length=2 new offset=3 length=2\nregion old offset=6 length=1 new offset=6 length=1\n"},
		{"shorter", []byte{0, 1, 2}, "region old offset=3 length=5 new offset=3 length=0\n"},
		{"longer", []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, "region old offset=8 length=0 new offset=8 length=2\n"},
		{"empty", []byte{}, "region old offset=0 length=8 new offset=0 length=0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			write(t, r.root, "binary", tc.data)
			id := committed(t, r, tc.name)
			var out bytes.Buffer
			if err := r.Diff(context.Background(), DiffOptions{Revisions: []string{first.String(), id.String()}}, &out); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("old binary size=%d sha256=%x\nnew binary size=%d sha256=%x\n%s", len(original), sha256.Sum256(original), len(tc.data), sha256.Sum256(tc.data), tc.regions)
			if !strings.HasSuffix(out.String(), want) {
				t.Fatalf("binary facts:\n%s\nwant suffix:\n%s", out.String(), want)
			}
		})
	}
}

func TestRevisionRestoreAlreadyMissingDirectory(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("concurrent-%t", concurrent), func(t *testing.T) {
			r, first, _ := revisionFixture(t)
			write(t, r.root, "nested/deep/file", []byte("later tracked"))
			head := committed(t, r, "nested addition")
			if err := os.RemoveAll(filepath.Join(r.root, "nested")); err != nil {
				t.Fatal(err)
			}
			if concurrent {
				injected := errors.New("stop with pinned missing directory")
				r.fault = func(stage, path string) error {
					if stage == "journal-published" {
						return injected
					}
					return nil
				}
				if err := r.RestoreFromContext(context.Background(), first.String(), "nested"); !errors.Is(err, injected) {
					t.Fatal(err)
				}
				r.fault = nil
				write(t, r.root, "nested/deep/file", []byte("new external child"))
				var aborted *RecoveryAbortedError
				if err := r.Recover(context.Background()); !errors.As(err, &aborted) {
					t.Fatal(err)
				}
				got, err := os.ReadFile(filepath.Join(r.root, "nested", "deep", "file"))
				if err != nil || string(got) != "new external child" {
					t.Fatal("later namespace overwritten", err)
				}
			} else {
				if err := r.RestoreFromContext(context.Background(), first.String(), "nested"); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(filepath.Join(r.root, "nested")); !os.IsNotExist(err) {
					t.Fatal("deletion created a directory", err)
				}
			}
			actual, err := r.CurrentID()
			if err != nil || actual != head {
				t.Fatal("missing-path restore moved history", err)
			}
		})
	}
}

func TestRevisionLiveDiffPreservedMetadata(t *testing.T) {
	ctx := context.Background()
	t.Run("executable", func(t *testing.T) {
		if measuresExecutable {
			t.Skip("unobservable executable history regression requires a host without executable measurement")
		}
		r, err := Init(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		write(t, r.root, "script", []byte("original\n"))
		committed(t, r, "base")
		entries, err := r.TreeEntries()
		if err != nil {
			t.Fatal(err)
		}
		entries[0].Executable = true
		filesystemBranch(t, r, "executable", entries)
		if err = r.store.UpdateHead(ctx, "heads/main", "heads/executable"); err != nil {
			t.Fatal(err)
		}
		before := metadataSnapshot(t, r.root)
		var out bytes.Buffer
		if err = r.Diff(ctx, DiffOptions{}, &out); err != nil || out.Len() != 0 {
			t.Fatalf("unchanged preserved executable diff: %q %v", out.String(), err)
		}
		write(t, r.root, "script", []byte("edited\n"))
		if err = r.Diff(ctx, DiffOptions{}, &out); err != nil || !strings.Contains(out.String(), "old mode executable\nnew mode executable\n") || !strings.HasSuffix(out.String(), "-original\n+edited\n") {
			t.Fatalf("edited preserved executable diff: %q %v", out.String(), err)
		}
		if !reflect.DeepEqual(before, metadataSnapshot(t, r.root)) {
			t.Fatal("live diff changed preserved metadata")
		}
	})
	t.Run("directory-link", func(t *testing.T) {
		r, err := Init(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if !requireFixtureLinks(t, r.root) {
			t.Skip("native symlinks unavailable")
		}
		committed(t, r, "base")
		manifest, err := r.store.IngestFile(ctx, strings.NewReader("missing-directory"), nil)
		if err != nil {
			t.Fatal(err)
		}
		filesystemBranch(t, r, "hint", []storev2.TreeEntry{{Path: "hint", Symlink: true, DirectoryLink: true, Manifest: manifest}})
		if err = r.Checkout("hint"); err != nil {
			t.Fatal(err)
		}
		before := metadataSnapshot(t, r.root)
		var out bytes.Buffer
		if err = r.Diff(ctx, DiffOptions{}, &out); err != nil || out.Len() != 0 {
			t.Fatalf("unchanged directory-hint diff: %q %v", out.String(), err)
		}
		if err = os.Remove(filepath.Join(r.root, "hint")); err != nil {
			t.Fatal(err)
		}
		if err = createLink("edited-directory", filepath.Join(r.root, "hint"), true); err != nil {
			t.Fatal(err)
		}
		if err = r.Diff(ctx, DiffOptions{}, &out); err != nil || !strings.Contains(out.String(), "old mode directory-link\nnew mode directory-link\n") || !strings.Contains(out.String(), "-missing-directory\n") || !strings.Contains(out.String(), "+edited-directory\n") {
			t.Fatalf("edited directory-hint diff: %q %v", out.String(), err)
		}
		if !reflect.DeepEqual(before, metadataSnapshot(t, r.root)) {
			t.Fatal("live diff changed directory hint metadata")
		}
	})
}

func TestRevisionRestoreStagedParentDeletion(t *testing.T) {
	for _, selection := range []string{"node", "."} {
		for _, point := range []string{"", "journal-published", "retain-published", "head-published", "later-file", "later-directory"} {
			t.Run(selection+"/"+point, func(t *testing.T) {
				r, source, _ := revisionFixture(t)
				write(t, r.root, "node/deep/child", []byte("HEAD child"))
				head := committed(t, r, "HEAD directory")
				if err := os.RemoveAll(filepath.Join(r.root, "node")); err != nil {
					t.Fatal(err)
				}
				write(t, r.root, "node", []byte("staged parent"))
				if err := r.Stage([]string{"node"}); err != nil {
					t.Fatal(err)
				}
				write(t, r.root, "node", []byte("dirty tracked parent"))
				write(t, r.root, "precious", []byte("untracked survives"))
				index, err := os.ReadFile(filepath.Join(r.root, ".pebble", "index"))
				if err != nil {
					t.Fatal(err)
				}
				stop := errors.New("interrupt staged-parent deletion")
				boundary := point
				if strings.HasPrefix(point, "later-") {
					boundary = "head-published"
				}
				r.fault = func(stage, path string) error {
					if boundary != "" && stage == boundary {
						return stop
					}
					return nil
				}
				err = r.RestoreFromContext(context.Background(), source.String(), selection)
				if point == "" && err != nil || point != "" && !errors.Is(err, stop) {
					t.Fatalf("restore staged parent at %s: %v", point, err)
				}
				r, err = Open(r.root)
				if err != nil {
					t.Fatal(err)
				}
				if point == "later-file" {
					write(t, r.root, "node", []byte("later external file"))
				}
				if point == "later-directory" {
					write(t, r.root, "node/foreign", []byte("later external child"))
				}
				err = r.Recover(context.Background())
				if strings.HasPrefix(point, "later-") {
					var aborted *RecoveryAbortedError
					if !errors.As(err, &aborted) {
						t.Fatalf("later entry not safely aborted: %v", err)
					}
					name, want := "node", "later external file"
					if point == "later-directory" {
						name, want = "node/foreign", "later external child"
					}
					got, e := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(name)))
					if e != nil || string(got) != want {
						t.Fatalf("later entry changed: %q %v", got, e)
					}
					receipts, e := r.RecoveryReceipts()
					if e != nil {
						t.Fatal(e)
					}
					found := false
					for _, receipt := range receipts {
						for _, version := range receipt.Versions {
							data, e := os.ReadFile(version.Location)
							if e == nil && string(data) == "dirty tracked parent" {
								found = true
							}
						}
					}
					if !found {
						t.Fatal("discarded parent preimage not discoverable")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if _, err = os.Lstat(filepath.Join(r.root, "node")); !os.IsNotExist(err) {
						t.Fatalf("selected parent was not deleted: %v", err)
					}
				}
				if err = r.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
				gotIndex, err := os.ReadFile(filepath.Join(r.root, ".pebble", "index"))
				if err != nil || !bytes.Equal(index, gotIndex) {
					t.Fatal("staged deletion changed index", err)
				}
				gotHead, err := r.CurrentID()
				if err != nil || gotHead != head {
					t.Fatal("staged deletion moved HEAD", gotHead, err)
				}
				for name, want := range map[string]string{"untouched": "safe", "precious": "untracked survives"} {
					got, e := os.ReadFile(filepath.Join(r.root, name))
					if e != nil || string(got) != want {
						t.Fatalf("unrelated %s changed: %q %v", name, got, e)
					}
				}
			})
		}
	}
	// A blocker is not selected/tracked merely because a tracked child exists.
	r, source, _ := revisionFixture(t)
	write(t, r.root, "node/child", []byte("tracked child"))
	committed(t, r, "directory")
	if err := os.RemoveAll(filepath.Join(r.root, "node")); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "node", []byte("untracked blocker"))
	before := metadataSnapshot(t, r.root)
	if err := r.RestoreFromContext(context.Background(), source.String(), "node"); err == nil {
		t.Fatal("untracked blocking parent accepted")
	}
	got, err := os.ReadFile(filepath.Join(r.root, "node"))
	if err != nil || string(got) != "untracked blocker" || !reflect.DeepEqual(before, metadataSnapshot(t, r.root)) {
		t.Fatal("refusal changed untracked blocker or metadata", err)
	}
}
