package repository

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

const recoveryBaseline = "5c4c4532f1af8d2ea013d32fb190019961969583"

const oldRecoveryArchiveConsumer = `package repository
import("bufio";"context";"fmt";"os";"strings";"testing")
func pb10Wait(){fmt.Println("READY");if _,err:=bufio.NewReader(os.Stdin).ReadString('\n');err!=nil{panic(err)}}
func pb10Prelock(){if os.Getenv("PEBBLE_OLD_PRELOCK")=="1"{pb10Wait()}}
func TestPB10OldConsumer(t *testing.T){
 r,err:=Open(os.Getenv("PEBBLE_OLD_ROOT"));if err!=nil{t.Fatal(err)}
 action:=os.Getenv("PEBBLE_OLD_ACTION")
 if action!="prelock"{pb10Wait()}
 if action=="reject-recovery"{if err=r.Recover(context.Background());err==nil||!strings.Contains(err.Error(),"unsupported or corrupt worktree journal"){t.Fatal("old held recovery did not reject new semantics",err)};fmt.Println("REJECTED_V3");return}
 if _,err=r.Commit("old writer after automatic recovery","Fixture","fixture@example.invalid");err!=nil{t.Fatal(err)}
 entries,err:=r.TreeEntries();if err!=nil{t.Fatal(err)}
 expected:=map[string]bool{"pebble-recovered-user/data":false,"PEBBLE-RECOVERED-History/Case":false,"pebble-recovered-東京/Σ":false,"worktree-stage-history/data":false}
 for _,entry:=range entries{if entry.Path==".pebble"||strings.HasPrefix(entry.Path,".pebble/"){t.Fatal("old writer captured private recovery archive",entry.Path)};if _,ok:=expected[entry.Path];ok{expected[entry.Path]=true}}
 for path,found:=range expected{if !found{t.Fatal("old writer hid ordinary lookalike path",path)}}
 fmt.Println("OLD_COMMIT_EXCLUDES_ARCHIVE")
}
func TestPB10OldRecoveryProducer(t *testing.T){
 r,err:=Open(os.Getenv("PEBBLE_OLD_ROOT"));if err!=nil{t.Fatal(err)}
 r.fault=func(stage,path string)error{if stage==os.Getenv("PEBBLE_OLD_STAGE"){os.Exit(73)};return nil}
 if err=r.Restore("a.bin");err!=nil{t.Fatal(err)}
 t.Fatal("old restore did not reach requested real interruption")
}
`

func recoveryLookalikes(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{
		"pebble-recovered-user/data":    []byte("ordinary same-prefix data"),
		"PEBBLE-RECOVERED-History/Case": []byte("ordinary case-sensitive spelling"),
		"pebble-recovered-東京/Σ":         []byte("ordinary Unicode history"),
		"worktree-stage-history/data":   []byte("ordinary historical transaction-like name"),
	}
	for name, data := range files {
		write(t, root, name, data)
	}
	return files
}

func assertArchiveExcludedTree(t *testing.T, r *Repository, expected map[string][]byte) storev2.ID {
	t.Helper()
	entries, err := r.TreeEntries()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Path == ".pebble" || strings.HasPrefix(entry.Path, ".pebble/") {
			t.Fatalf("recovery archive entered preserved user tree: %s", entry.Path)
		}
		if data, ok := expected[entry.Path]; ok {
			got, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(entry.Path)))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("lookalike user bytes lost: %s %q %v", entry.Path, got, err)
			}
			seen[entry.Path] = true
		}
	}
	for name := range expected {
		if !seen[name] {
			t.Fatalf("ordinary lookalike/history path silently omitted: %s", name)
		}
	}
	id, err := r.CurrentID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAutomaticRecoveryArchiveScanCommitHistory(t *testing.T) {
	f := pauseCheckout(t, "install-published")
	write(t, f.r.root, "a.bin", []byte("later public bytes"))
	receipt := automaticAbort(t, f.r, "heads/before")
	beforeArchive := publicSnapshot(t, receipt.Directory)
	expected := recoveryLookalikes(t, f.r.root)
	changes, err := f.r.Status()
	if err != nil {
		t.Fatal(err)
	}
	for name := range expected {
		found := false
		for _, change := range changes {
			if change.Path == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("status hid ordinary user lookalike %s: %#v", name, changes)
		}
	}
	committed(t, f.r, "capture ordinary lookalikes, never recovery metadata")
	preserved := assertArchiveExcludedTree(t, f.r, expected)
	if err = f.r.CreateBranch("lookalike-history"); err != nil {
		t.Fatal(err)
	}
	for name := range expected {
		write(t, f.r.root, name, []byte("later ordinary version"))
	}
	committed(t, f.r, "later ordinary versions")
	if err = f.r.Checkout("lookalike-history"); err != nil {
		t.Fatal(err)
	}
	if got := assertArchiveExcludedTree(t, f.r, expected); got != preserved {
		t.Fatalf("historical lookalike identity changed: %s want %s", got, preserved)
	}
	assertPublicSnapshot(t, receipt.Directory, beforeArchive)
	if _, err = f.r.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticRecoveryOldConsumers(t *testing.T) {
	source := os.Getenv("PEBBLE_TEST_RECOVERY_OLD_SOURCE")
	if source == "" {
		if os.Getenv("PEBBLE_REQUIRE_RECOVERY_OLD_CONSUMER") == "1" {
			t.Fatal("required actual5c4 frozen source is unavailable")
		}
		t.Skip("actual5c4 consumer proof requires generated frozen source; native CI supplies it")
	}
	if !strings.HasPrefix(filepath.Base(source), "pebble-pb10-recovery-old-") {
		t.Fatal("recovery old-consumer source must be a generated temporary fixture")
	}
	baseline, err := os.ReadFile(filepath.Join(source, "BASELINE"))
	if err != nil || strings.TrimSpace(string(baseline)) != recoveryBaseline {
		t.Fatalf("wrong frozen recovery baseline: %q %v", baseline, err)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go"+suffix)
	oldCLI := filepath.Join(t.TempDir(), "pebble5c4"+suffix)
	build := exec.Command(goExe, "build", "-o", oldCLI, "./cmd/pebble")
	build.Dir = source
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual unmodified5c4 CLI: %v\n%s", err, out)
	}
	bin := buildOldConsumer(t, source, "internal/repository", oldRecoveryArchiveConsumer, `unlock, err := processlock.Acquire(filepath.Join(r.root, ".pebble", "worktree.guard"))`)
	for _, action := range []string{"held", "prelock", "post-retirement", "reject-recovery"} {
		t.Run(action, func(t *testing.T) {
			f := newRecoveryFixture(t)
			expected := recoveryLookalikes(t, f.r.root)
			marker, err := os.ReadFile(filepath.Join(f.r.root, ".pebble", "format"))
			if err != nil {
				t.Fatal(err)
			}
			var consumer *runningOldConsumer
			if action != "post-retirement" {
				consumer = startOldConsumer(t, bin, f.r.root, action, action == "prelock")
			}
			interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_STAGE=install-published")
			write(t, f.r.root, "a.bin", []byte("later external bytes must stay canonical"))
			reopenRecovery(t, &f)
			if action == "reject-recovery" {
				before := fixtureSnapshot(t, f.r.root)
				consumer.resume(t, "REJECTED_V3")
				if after := fixtureSnapshot(t, f.r.root); !reflect.DeepEqual(before, after) {
					t.Fatal("already-open old recovery mutated v3 intent, bytes or HEAD")
				}
				cmd := exec.Command(oldCLI, "recover")
				cmd.Dir = f.r.root
				if out, err := cmd.CombinedOutput(); err == nil || !bytes.Contains(out, []byte("unsupported or corrupt worktree journal")) {
					t.Fatalf("actual old CLI accepted v3: %v\n%s", err, out)
				}
				if after := fixtureSnapshot(t, f.r.root); !reflect.DeepEqual(before, after) {
					t.Fatal("old CLI recovery mutated v3 state")
				}
				automaticAbort(t, f.r, "heads/before")
				return
			}
			receipt := automaticAbort(t, f.r, "heads/before")
			archive := publicSnapshot(t, receipt.Directory)
			if action == "post-retirement" {
				consumer = startOldConsumer(t, bin, f.r.root, action, false)
			}
			consumer.resume(t, "OLD_COMMIT_EXCLUDES_ARCHIVE")
			reopenRecovery(t, &f)
			assertArchiveExcludedTree(t, f.r, expected)
			assertPublicSnapshot(t, receipt.Directory, archive)
			if after, err := os.ReadFile(filepath.Join(f.r.root, ".pebble", "format")); err != nil || !bytes.Equal(after, marker) {
				t.Fatalf("ordinary old/new recovery implicitly upgraded repository marker: %q %v", after, err)
			}
			// Exercise the independently built old CLI after retirement too.
			expected["cli-after-recovery"] = []byte("ordinary user edit for the independently built CLI")
			write(t, f.r.root, "cli-after-recovery", expected["cli-after-recovery"])
			cmd := exec.Command(oldCLI, "commit", "old CLI archive exclusion")
			cmd.Dir = f.r.root
			cmd.Env = append(os.Environ(), "PEBBLE_AUTHOR=Disposable Fixture", "PEBBLE_EMAIL=fixture@example.invalid")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("actual old CLI post-retirement commit: %v\n%s", err, out)
			}
			reopenRecovery(t, &f)
			assertArchiveExcludedTree(t, f.r, expected)
			assertPublicSnapshot(t, receipt.Directory, archive)
			if _, err = f.r.Verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, point := range []string{"journal-published", "history-backed-original", "retain-published", "install-published", "head-published", "repair-upgrade-preserved", "repair-upgrade", "repair-upgrade-published"} {
		t.Run("actual-v2/"+point, func(t *testing.T) {
			f := newRecoveryFixture(t)
			dirty := []byte("dirty original from actual5c4 restore")
			if point == "history-backed-original" {
				dirty = f.old["a.bin"]
			}
			write(t, f.r.root, "a.bin", dirty)
			oldStage := point
			if strings.HasPrefix(point, "repair-") {
				oldStage = "install-published"
			}
			if point == "history-backed-original" {
				oldStage = "journal-published"
			}
			producer := exec.Command(bin, "-test.run=^TestPB10OldRecoveryProducer$", "-test.v")
			producer.Env = append(os.Environ(), "PEBBLE_OLD_ROOT="+f.r.root, "PEBBLE_OLD_STAGE="+oldStage)
			if out, err := producer.CombinedOutput(); err == nil {
				t.Fatalf("actual old process unexpectedly completed: %s", out)
			} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 73 {
				t.Fatalf("actual old interruption: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(filepath.Join(f.r.journalPath(), "intent.json"))
			if err != nil {
				t.Fatal(err)
			}
			later := []byte("later public bytes survive actual legacy recovery")
			write(t, f.r.root, "a.bin", later)
			public := publicSnapshot(t, f.r.root)
			if point == "journal-published" {
				// Old code never saved these dirty bytes before retain. They
				// were already overwritten; recovery cannot fabricate them.
				for i := 0; i < 2; i++ {
					reopenRecovery(t, &f)
					if err = f.r.Recover(context.Background()); err == nil || !strings.Contains(err.Error(), "no available original preimage") {
						t.Fatalf("legacy missing original falsely completed: %v", err)
					}
					assertPublicSnapshot(t, f.r.root, public)
					if got, err := os.ReadFile(filepath.Join(f.r.journalPath(), "intent.json")); err != nil || !bytes.Equal(got, raw) {
						t.Fatalf("unrecoverable legacy envelope changed/deleted: %v", err)
					}
					if head, err := f.r.CurrentID(); err != nil || head != f.before {
						t.Fatalf("missing legacy original advanced HEAD: %s %v", head, err)
					}
				}
				t.Log("honest legacy boundary: old code never saved the subsequently overwritten dirty preimage; operation remains pending")
				return
			}
			if strings.HasPrefix(point, "repair-") {
				interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_RECOVER=1", "PEBBLE_TEST_WORKTREE_STAGE="+point)
			}
			reopenRecovery(t, &f)
			receipt := automaticAbort(t, f.r, "heads/before")
			preservedVersion(t, receipt, "a.bin", "original", dirty)
			preservedVersion(t, receipt, ".pebble/worktree-journal/intent.json", "legacy-intent", raw)
			assertReconciledPublic(t, f.r, public, receipt)
			for i := 0; i < 2; i++ {
				reopenRecovery(t, &f)
				if err = f.r.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
				assertPublicSnapshot(t, f.r.root, public)
			}
		})
	}
}
