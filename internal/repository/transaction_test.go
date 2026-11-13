package repository

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/suttonwilliamd/pebble/internal/storev2"
	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

type recoveryFixture struct {
	r             *Repository
	before, after storev2.ID
	old, target   map[string][]byte
}

func newRecoveryFixture(t *testing.T) recoveryFixture {
	t.Helper()
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := map[string][]byte{"a.bin": {0, 1, 2, 255, '\n', 0}, "removed": []byte("old tracked removal\x00")}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err = gz.Write(bytes.Repeat([]byte("generated compressed bytes\x00"), 128)); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	target := map[string][]byte{"a.bin": {255, 0, 3, 2, 1, 0}, "nested/deep/new.gz": compressed.Bytes()}
	for name, data := range old {
		write(t, r.root, name, data)
	}
	before := committed(t, r, "before")
	if err = r.CreateBranch("before"); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(r.root, "removed")); err != nil {
		t.Fatal(err)
	}
	for name, data := range target {
		write(t, r.root, name, data)
	}
	after := committed(t, r, "after")
	if err = r.store.UpdateHead(context.Background(), "heads/main", "heads/before"); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(filepath.Join(r.root, "nested")); err != nil {
		t.Fatal(err)
	}
	for name, data := range old {
		write(t, r.root, name, data)
	}
	return recoveryFixture{r, before, after, old, target}
}
func assertRecoveryState(t *testing.T, f recoveryFixture, after bool) {
	t.Helper()
	want, id, head := f.old, f.before, "heads/before"
	if after {
		want, id, head = f.target, f.after, "heads/main"
	}
	for name := range map[string]bool{"a.bin": true, "removed": true, "nested/deep/new.gz": true} {
		got, err := os.ReadFile(filepath.Join(f.r.root, filepath.FromSlash(name)))
		data, exists := want[name]
		if !exists {
			if !os.IsNotExist(err) {
				t.Fatalf("unexpected file %s: %q %v", name, got, err)
			}
			continue
		}
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("bytes %s: got %x want %x: %v", name, got, data, err)
		}
	}
	gotHead, err := f.r.GetHead()
	if err != nil || gotHead != head {
		t.Fatalf("HEAD: %s want %s: %v", gotHead, head, err)
	}
	gotID, err := f.r.CurrentID()
	if err != nil || gotID != id {
		t.Fatalf("HEAD revision: %s want %s: %v", gotID, id, err)
	}
	changes, err := f.r.Status()
	if err != nil || len(changes) != 0 {
		t.Fatalf("HEAD-derived index differs: %v %v", changes, err)
	}
}
func reopenRecovery(t *testing.T, f *recoveryFixture) {
	t.Helper()
	var err error
	f.r, err = Open(f.r.root)
	if err != nil {
		t.Fatal(err)
	}
}
func journalExists(t *testing.T, r *Repository) bool {
	t.Helper()
	_, err := os.Lstat(r.journalPath())
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}
func checkoutBoundaries(t *testing.T) []string {
	t.Helper()
	f := newRecoveryFixture(t)
	var stages []string
	f.r.fault = func(stage, path string) error { stages = append(stages, stage); return nil }
	if err := f.r.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	f.r.fault = nil
	assertRecoveryState(t, f, true)
	return stages
}

func TestCheckoutRecoveryEveryFaultBoundary(t *testing.T) {
	stages := checkoutBoundaries(t)
	for at, stage := range stages {
		t.Run(fmt.Sprintf("%02d-%s", at+1, stage), func(t *testing.T) {
			f := newRecoveryFixture(t)
			injected := errors.New("injected filesystem failure")
			hit := 0
			f.r.fault = func(_, _ string) error {
				hit++
				if hit == at+1 {
					return injected
				}
				return nil
			}
			if err := f.r.Checkout("main"); !errors.Is(err, injected) {
				t.Fatalf("fault not propagated: %v", err)
			}
			pending := journalExists(t, f.r)
			if pending {
				if _, err := f.r.Commit("must not record partial files", "Fixture", "fixture@example.com"); err == nil {
					t.Fatal("commit admitted during recovery")
				}
				if _, err := f.r.Status(); err == nil {
					t.Fatal("status treated pending tree as coherent")
				}
			}
			reopenRecovery(t, &f)
			for i := 0; i < 2; i++ {
				if err := f.r.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			after := pending || strings.HasPrefix(stage, "complete")
			assertRecoveryState(t, f, after)
		})
	}
}

// Only child processes honor these environment variables. No production crash
// switch is shipped. Termination bypasses deferred cleanup, not OS storage caches.
func TestWorktreeProcessHelper(t *testing.T) {
	root := os.Getenv("PEBBLE_TEST_WORKTREE_ROOT")
	if root == "" {
		return
	}
	r, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PEBBLE_TEST_WORKTREE_LOCKED") == "1" {
		if err = r.Recover(context.Background()); err == nil {
			t.Fatal("live worktree guard bypassed")
		}
		return
	}
	at, _ := strconv.Atoi(os.Getenv("PEBBLE_TEST_WORKTREE_AT"))
	stage := os.Getenv("PEBBLE_TEST_WORKTREE_STAGE")
	hit := 0
	r.fault = func(point, path string) error {
		hit++
		if at == hit || stage != "" && point == stage {
			if os.Getenv("PEBBLE_TEST_WORKTREE_KILL") == "1" {
				fmt.Println("interruption-ready")
				var b [1]byte
				_, _ = os.Stdin.Read(b[:])
			}
			os.Exit(73)
		}
		return nil
	}
	if os.Getenv("PEBBLE_TEST_WORKTREE_RECOVER") == "1" {
		err = r.Recover(context.Background())
	} else if os.Getenv("PEBBLE_TEST_WORKTREE_RESTORE") == "1" {
		name := os.Getenv("PEBBLE_TEST_WORKTREE_FILE")
		if name == "" {
			name = "a.bin"
		}
		err = r.Restore(name)
	} else {
		branch := os.Getenv("PEBBLE_TEST_WORKTREE_BRANCH")
		if branch == "" {
			branch = "main"
		}
		err = r.Checkout(branch)
	}
	t.Fatal("interruption boundary not reached:", err)
}
func worktreeChild(t *testing.T, root string, env ...string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestWorktreeProcessHelper$", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), append([]string{"PEBBLE_TEST_WORKTREE_ROOT=" + root}, env...)...)
	return cmd
}
func interruptWorktree(t *testing.T, root string, env ...string) {
	t.Helper()
	out, err := worktreeChild(t, root, env...).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatalf("child interruption: %v\n%s", err, out)
	}
}
func TestCheckoutRecoveryEveryProcessBoundary(t *testing.T) {
	for at, stage := range checkoutBoundaries(t) {
		t.Run(fmt.Sprintf("%02d-%s", at+1, stage), func(t *testing.T) {
			f := newRecoveryFixture(t)
			interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_AT="+strconv.Itoa(at+1))
			pending := journalExists(t, f.r)
			reopenRecovery(t, &f)
			for i := 0; i < 2; i++ {
				if err := f.r.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			after := pending || strings.HasPrefix(stage, "complete")
			assertRecoveryState(t, f, after)
		})
	}
}
func TestKilledCheckoutAndBuiltCLIRecovery(t *testing.T) {
	f := newRecoveryFixture(t)
	cmd := worktreeChild(t, f.r.root, "PEBBLE_TEST_WORKTREE_STAGE=install-published", "PEBBLE_TEST_WORKTREE_KILL=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		ready <- scanner.Scan() && scanner.Text() == "interruption-ready"
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("child did not pause at installed-file boundary")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("child did not reach boundary")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("killed process succeeded")
	}
	binary := filepath.Join(t.TempDir(), "pebble")
	goName := "go"
	if runtime.GOOS == "windows" {
		binary += ".exe"
		goName += ".exe"
	}
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", goName), "build", "-o", binary, "../../cmd/pebble")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	invoke := func(ok bool, args ...string) string {
		t.Helper()
		c := exec.Command(binary, args...)
		c.Dir = f.r.root
		out, err := c.CombinedOutput()
		if (err == nil) != ok {
			t.Fatalf("CLI %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	invoke(false, "checkout", "main")
	invoke(false, "status")
	for i := 0; i < 2; i++ {
		invoke(true, "recover")
	}
	if out := invoke(true, "status"); !strings.Contains(out, "Working tree clean") || !strings.Contains(out, "On branch main") {
		t.Fatal(out)
	}
	invoke(true, "fsck")
	reopenRecovery(t, &f)
	assertRecoveryState(t, f, true)
	// Corrupt recovery input must fail in the actual executable too.
	if err = os.Mkdir(f.r.journalPath(), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, f.r.root, ".pebble/worktree-journal/intent.json", []byte("{broken"))
	invoke(false, "recover")
	for name, data := range f.target {
		got, err := os.ReadFile(filepath.Join(f.r.root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("CLI corrupted %s: %v", name, err)
		}
	}
}

func pauseCheckout(t *testing.T, stage string) recoveryFixture {
	t.Helper()
	f := newRecoveryFixture(t)
	interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_STAGE="+stage)
	reopenRecovery(t, &f)
	return f
}
func TestRecoveryPreservesExternalEdits(t *testing.T) {
	for _, which := range []string{"original", "installed", "gap", "untracked", "identical-replacement", "directory", "case-alias"} {
		t.Run(which, func(t *testing.T) {
			stage := "journal-published"
			if which == "installed" || which == "identical-replacement" {
				stage = "install-published"
			}
			if which == "gap" {
				stage = "retain-published"
			}
			if which == "directory" {
				stage = "directory-published"
			}
			f := pauseCheckout(t, stage)
			path := "a.bin"
			precious := []byte("post-interruption uncommitted\x00bytes")
			if which == "untracked" {
				path = "nested/deep/new.gz"
			}
			if which == "identical-replacement" {
				precious = f.target["a.bin"]
				// Keep the transaction-owned identity alive so inode reuse cannot mask the test.
				if err := os.Rename(filepath.Join(f.r.root, "a.bin"), filepath.Join(f.r.root, "saved-owned")); err != nil {
					t.Fatal(err)
				}
			}
			if which == "case-alias" {
				if err := os.Rename(filepath.Join(f.r.root, "a.bin"), filepath.Join(f.r.root, "A.bin")); err != nil {
					t.Fatal(err)
				}
				path = "A.bin"
			}
			if which == "directory" {
				if err := os.Rename(filepath.Join(f.r.root, "nested"), filepath.Join(f.r.root, "saved-directory")); err != nil {
					t.Fatal(err)
				}
				path = "nested/precious"
			}
			write(t, f.r.root, path, precious)
			head, err := f.r.GetHead()
			if err != nil {
				t.Fatal(err)
			}
			before := publicSnapshot(t, f.r.root)
			var restored []string
			switch which {
			case "installed", "identical-replacement":
				restored = []string{"removed"}
			case "directory":
				restored = []string{"a.bin", "removed"}
			}
			identities := map[string]string{}
			plan, err := f.r.readIntent()
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range restored {
				for i, file := range plan.Files {
					if file.Path == name {
						identities[name], err = filesystemIdentity(filepath.Join(f.r.journalPath(), retainedName(i)))
						if err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			receipt := automaticAbort(t, f.r, head)
			assertReconciledPublic(t, f.r, before, receipt, restored...)
			for _, name := range restored {
				got, err := os.ReadFile(filepath.Join(f.r.root, name))
				if err != nil || !bytes.Equal(got, f.old[name]) {
					t.Fatalf("automatic original restoration %s: %x %v", name, got, err)
				}
				if id, err := filesystemIdentity(filepath.Join(f.r.root, name)); err != nil || id != identities[name] {
					t.Fatalf("automatic original inode %s: %s want %s: %v", name, id, identities[name], err)
				}
			}
			preservedVersion(t, receipt, "a.bin", "original", f.old["a.bin"])
			for i := 0; i < 2; i++ {
				reopenRecovery(t, &f)
				if err = f.r.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(filepath.Join(f.r.root, filepath.FromSlash(path)))
				if err != nil || !bytes.Equal(got, precious) {
					t.Fatalf("external bytes lost: %x %v", got, err)
				}
				gotHead, err := f.r.GetHead()
				if err != nil || gotHead != head {
					t.Fatalf("HEAD changed on conflict: %s %v", gotHead, err)
				}
			}
		})
	}
}
func TestRestoreRecoveryRetainsDirtyOriginal(t *testing.T) {
	for _, stage := range []string{"journal-published", "retain-published", "install-published", "namespace-sync", "head-published"} {
		t.Run(stage, func(t *testing.T) {
			f := newRecoveryFixture(t)
			dirty := []byte("explicitly discarded edits\x00\xff")
			write(t, f.r.root, "a.bin", dirty)
			interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_RESTORE=1", "PEBBLE_TEST_WORKTREE_STAGE="+stage)
			reopenRecovery(t, &f)
			if stage != "journal-published" {
				retained, err := os.ReadFile(filepath.Join(f.r.journalPath(), retainedName(0)))
				if err != nil || !bytes.Equal(retained, dirty) {
					t.Fatalf("dirty original not retained: %x %v", retained, err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := f.r.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			assertRecoveryState(t, f, false)
		})
	}
}
func TestRestoreRecoveryRejectsLaterEdit(t *testing.T) {
	f := newRecoveryFixture(t)
	write(t, f.r.root, "a.bin", []byte("discarded at start"))
	interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_RESTORE=1", "PEBBLE_TEST_WORKTREE_STAGE=install-published")
	precious := []byte("new edits after interrupted restore")
	write(t, f.r.root, "a.bin", precious)
	reopenRecovery(t, &f)
	if err := f.r.Recover(context.Background()); err == nil {
		t.Fatal("later restore edit accepted")
	}
	got, err := os.ReadFile(filepath.Join(f.r.root, "a.bin"))
	if err != nil || !bytes.Equal(got, precious) {
		t.Fatalf("later edit lost: %q %v", got, err)
	}
}
func TestRecoveryRejectsCorruptOrIncompleteJournal(t *testing.T) {
	for _, damage := range []string{"truncated", "checksum", "unsupported", "missing-intent", "missing-stage", "duplicate-path", "missing-directory", "symlink-intent"} {
		t.Run(damage, func(t *testing.T) {
			f := pauseCheckout(t, "journal-published")
			path := filepath.Join(f.r.journalPath(), "intent.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := f.r.readIntent()
			if err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "truncated":
				data = data[:len(data)/2]
			case "checksum":
				data = bytes.Replace(data, []byte(`"SHA256":"`), []byte(`"SHA256":"bad`), 1)
			case "unsupported":
				var envelope journalEnvelope
				if err = json.Unmarshal(data, &envelope); err != nil {
					t.Fatal(err)
				}
				envelope.Version = 0
				data, err = json.Marshal(envelope)
			case "missing-intent":
				err = os.Remove(path)
			case "missing-stage":
				err = os.Remove(filepath.Join(f.r.journalPath(), stageName(0)))
			case "missing-directory":
				err = os.Remove(filepath.Join(f.r.journalPath(), directoryName(0)))
			case "duplicate-path":
				plan.Files = append(plan.Files, plan.Files[0])
				err = writeIntent(f.r.journalPath(), plan)
			case "symlink-intent":
				saved := filepath.Join(t.TempDir(), "intent.json")
				if err = os.Rename(path, saved); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(saved, path); err != nil {
					t.Skipf("symlink creation unavailable: %v", err)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if damage == "truncated" || damage == "checksum" || damage == "unsupported" {
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err = f.r.Recover(context.Background()); err == nil {
					t.Fatalf("accepted %s journal", damage)
				}
			}
			for name, want := range f.old {
				got, err := os.ReadFile(filepath.Join(f.r.root, name))
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("corruption changed %s: %v", name, err)
				}
			}
			head, err := f.r.GetHead()
			if err != nil || head != "heads/before" {
				t.Fatalf("HEAD on journal error: %s %v", head, err)
			}
		})
	}
}
func TestRecoveryCancellationAndLiveSerialization(t *testing.T) {
	for _, stage := range []string{"prepare-file", "journal-published", "install-published", "head"} {
		t.Run(stage, func(t *testing.T) {
			f := newRecoveryFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.r.fault = func(point, path string) error {
				if point == stage {
					cancel()
					return ctx.Err()
				}
				return nil
			}
			if err := f.r.CheckoutContext(ctx, "main"); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			pending := journalExists(t, f.r)
			reopenRecovery(t, &f)
			if err := f.r.Recover(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled recovery: %v", err)
			}
			if err := f.r.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertRecoveryState(t, f, pending)
		})
	}
	f := newRecoveryFixture(t)
	unlock, err := f.r.operationLock()
	if err != nil {
		t.Fatal(err)
	}
	out, childErr := worktreeChild(t, f.r.root, "PEBBLE_TEST_WORKTREE_LOCKED=1").CombinedOutput()
	if err = unlock(); err != nil {
		t.Fatal(err)
	}
	if childErr != nil {
		t.Fatalf("live exclusion: %v\n%s", childErr, out)
	}
	assertRecoveryState(t, f, false)
	// Old exclusive markers remain fail-closed rather than guessed stale.
	write(t, f.r.root, ".pebble/worktree.lock", []byte("legacy owner"))
	if err = f.r.Recover(context.Background()); err == nil {
		t.Fatal("legacy lock bypassed")
	}
}

func TestRecoveryPackedMixedRepeatedHistory(t *testing.T) {
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files, err := testfixture.Mixed(8 << 10)
	if err != nil {
		t.Fatal(err)
	}
	revisions := testfixture.Revisions(8 << 10)
	if err = testfixture.Materialize(r.root, files); err != nil {
		t.Fatal(err)
	}
	ids := map[string]storev2.ID{}
	for _, revision := range revisions {
		if revision.Name == "identical" {
			continue
		}
		if err = testfixture.Materialize(r.root, []testfixture.File{revision.File}); err != nil {
			t.Fatal(err)
		}
		ids[revision.Name] = committed(t, r, revision.Name)
		if err = r.CreateBranch(revision.Name); err != nil {
			t.Fatal(err)
		}
	}
	inventory, err := r.store.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.store.Pack(context.Background(), inventory); err != nil {
		t.Fatal(err)
	}
	for _, revision := range revisions {
		if revision.Name == "identical" {
			continue
		}
		// Always start from a different revision so an install is required.
		start := "original"
		if revision.Name == start {
			start = "empty"
		}
		if err = r.Checkout(start); err != nil {
			t.Fatal(err)
		}
		interruptWorktree(t, r.root, "PEBBLE_TEST_WORKTREE_BRANCH="+revision.Name, "PEBBLE_TEST_WORKTREE_STAGE=install-published")
		r, err = Open(r.root)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err = r.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		for _, file := range append(files[:len(files):len(files)], revision.File) {
			got, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(file.Path)))
			if err != nil || !bytes.Equal(got, file.Data) {
				t.Fatalf("packed recovery %s/%s: byte mismatch %v", revision.Name, file.Path, err)
			}
		}
		head, err := r.GetHead()
		if err != nil || head != "heads/"+revision.Name {
			t.Fatalf("packed recovery HEAD: %s %v", head, err)
		}
		id, err := r.CurrentID()
		if err != nil || id != ids[revision.Name] {
			t.Fatalf("packed recovery revision: %s %v", id, err)
		}
		changes, err := r.Status()
		if err != nil || len(changes) != 0 {
			t.Fatalf("packed recovery index: %v %v", changes, err)
		}
	}
}

func TestRestoreMissingNestedFileRecovery(t *testing.T) {
	for _, stage := range []string{"directory-published", "install-published"} {
		t.Run(stage, func(t *testing.T) {
			f := newRecoveryFixture(t)
			if err := f.r.Checkout("main"); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(f.r.root, "nested", "deep", "new.gz")); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"nested/deep", "nested"} {
				if err := os.Remove(filepath.Join(f.r.root, filepath.FromSlash(name))); err != nil {
					t.Fatal(err)
				}
			}
			interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_RESTORE=1", "PEBBLE_TEST_WORKTREE_FILE=nested/deep/new.gz", "PEBBLE_TEST_WORKTREE_STAGE="+stage)
			reopenRecovery(t, &f)
			if err := f.r.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertRecoveryState(t, f, true)
		})
	}
}

func TestRecoveryDoesNotOverwriteConcurrentHEAD(t *testing.T) {
	f := newRecoveryFixture(t)
	if err := f.r.CreateBranch("external"); err != nil {
		t.Fatal(err)
	}
	f.r.fault = func(stage, path string) error {
		if stage == "head" {
			return f.r.store.UpdateHead(context.Background(), "heads/before", "heads/external")
		}
		return nil
	}
	if err := f.r.Checkout("main"); err == nil {
		t.Fatal("transaction overwrote an external HEAD change")
	}
	reopenRecovery(t, &f)
	if err := f.r.Recover(context.Background()); err == nil {
		t.Fatal("recovery overwrote an external HEAD change")
	}
	head, err := f.r.GetHead()
	if err != nil || head != "heads/external" {
		t.Fatalf("external HEAD lost: %s %v", head, err)
	}
	id, err := f.r.CurrentID()
	if err != nil || id != f.before {
		t.Fatalf("external revision lost: %s %v", id, err)
	}
	for name, want := range f.target {
		got, err := os.ReadFile(filepath.Join(f.r.root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("conflict handling changed %s: %v", name, err)
		}
	}
}

func TestRestoreRecoveryEveryBoundary(t *testing.T) {
	dirty := []byte("discard only after durable intent\x00\xff")
	baseline := newRecoveryFixture(t)
	write(t, baseline.r.root, "a.bin", dirty)
	var stages []string
	baseline.r.fault = func(stage, path string) error {
		stages = append(stages, stage)
		return nil
	}
	if err := baseline.r.Restore("a.bin"); err != nil {
		t.Fatal(err)
	}
	baseline.r.fault = nil
	assertRecoveryState(t, baseline, false)
	for _, terminate := range []bool{false, true} {
		for at, stage := range stages {
			t.Run(fmt.Sprintf("process-%t/%02d-%s", terminate, at+1, stage), func(t *testing.T) {
				f := newRecoveryFixture(t)
				write(t, f.r.root, "a.bin", dirty)
				if terminate {
					interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_RESTORE=1", "PEBBLE_TEST_WORKTREE_AT="+strconv.Itoa(at+1))
				} else {
					injected := errors.New("restore filesystem failure")
					hit := 0
					f.r.fault = func(_, _ string) error {
						hit++
						if hit == at+1 {
							return injected
						}
						return nil
					}
					if err := f.r.Restore("a.bin"); !errors.Is(err, injected) {
						t.Fatalf("restore fault hidden: %v", err)
					}
				}
				applied := journalExists(t, f.r) || strings.HasPrefix(stage, "complete")
				reopenRecovery(t, &f)
				for i := 0; i < 2; i++ {
					if err := f.r.Recover(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				if applied {
					assertRecoveryState(t, f, false)
					return
				}
				got, err := os.ReadFile(filepath.Join(f.r.root, "a.bin"))
				if err != nil || !bytes.Equal(got, dirty) {
					t.Fatalf("pre-intent restore discarded dirty bytes: %x %v", got, err)
				}
				id, err := f.r.CurrentID()
				if err != nil || id != f.before {
					t.Fatalf("pre-intent restore changed revision: %s %v", id, err)
				}
				changes, err := f.r.Status()
				if err != nil || len(changes) != 1 || changes[0] != (Change{"M", "a.bin"}) {
					t.Fatalf("pre-intent dirty index view: %v %v", changes, err)
				}
			})
		}
	}
}

func TestRecoveryCanBeInterruptedRepeatedly(t *testing.T) {
	f := pauseCheckout(t, "journal-published")
	// Each resume advances to a later durable phase; retention now precedes
	// directory installation rather than being interleaved with leaf installs.
	for _, stage := range []string{"retain-published", "directory-published", "install-published", "head-published", "complete-published"} {
		interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_RECOVER=1", "PEBBLE_TEST_WORKTREE_STAGE="+stage)
		reopenRecovery(t, &f)
	}
	for i := 0; i < 2; i++ {
		if err := f.r.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	assertRecoveryState(t, f, true)
}
