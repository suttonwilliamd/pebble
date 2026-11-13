package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func automaticAbort(t *testing.T, r *Repository, head string) RecoveryReceipt {
	t.Helper()
	err := r.Recover(context.Background())
	var aborted *RecoveryAbortedError
	if !errors.As(err, &aborted) {
		t.Fatalf("expected automatically reconciled abort, not target success or manual repair: %v", err)
	}
	if journalExists(t, r) {
		t.Fatal("automatic reconciliation left an active journal")
	}
	if got, err := r.GetHead(); err != nil || got != head {
		t.Fatalf("aborted operation changed HEAD: %s %v", got, err)
	}
	receipts, err := r.RecoveryReceipts()
	if err != nil || len(receipts) != 1 || receipts[0].Directory != aborted.Directory {
		t.Fatalf("durable aborted-operation receipt: %#v %v", receipts, err)
	}
	if _, err = r.Verify(); err != nil {
		t.Fatal(err)
	}
	return receipts[0]
}

func preservedVersion(t *testing.T, receipt RecoveryReceipt, path, role string, data []byte) string {
	t.Helper()
	for _, version := range receipt.Versions {
		if version.Path != path || version.Role != role {
			continue
		}
		if version.Changed {
			t.Fatalf("preserved version already marked changed: %#v", version)
		}
		got, err := os.ReadFile(version.Location)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("preserved %s/%s: %q want %q: %v", path, role, got, data, err)
		}
		return version.Location
	}
	t.Fatalf("receipt lacks %s/%s: %#v", path, role, receipt)
	return ""
}

// Check exact type/mode/identity/link-target/bytes for EVERY existing public
// entry. Only explicitly expected vacant canonical restorations may add names.
// Recovery metadata uses the existing namespace, not a new prefix exemption.
func assertReconciledPublic(t *testing.T, r *Repository, before map[string]string, receipt RecoveryReceipt, restored ...string) {
	t.Helper()
	got := publicSnapshot(t, r.root)
	for name, want := range before {
		if got[name] != want {
			t.Fatalf("automatic recovery changed pre-existing public entry %s:\ngot %q\nwant %q", name, got[name], want)
		}
	}
	if filepath.Dir(receipt.Directory) != filepath.Join(r.root, ".pebble", "recovery", "archives") {
		t.Fatalf("recovery archive is not in the first-class metadata namespace: %s", receipt.Directory)
	}
	allowed := map[string]bool{}
	for _, name := range restored {
		allowed[filepath.FromSlash(name)] = true
	}
	for name := range got {
		if _, exists := before[name]; exists || allowed[name] {
			continue
		}
		t.Fatalf("unreceipted public mutation: %s", name)
	}
	// The archive must retain the original journal directory identity, not just
	// some similarly named directory containing copied bytes.
	var repair recoveryPlan
	if err := readRecoveryRecord(receipt.Directory, "repair.json", &repair); err != nil {
		t.Fatal(err)
	}
	id, err := filesystemIdentity(receipt.Directory)
	if err != nil || id != repair.JournalIdentity {
		t.Fatalf("archive directory ownership not preserved: %s %v", id, err)
	}
}

func TestAutomaticRecoveryDirtyPreimageBeforeRetain(t *testing.T) {
	for _, stage := range []string{"journal-published", "retain-published", "install-published", "head-published"} {
		t.Run(stage, func(t *testing.T) {
			f := newRecoveryFixture(t)
			dirty := []byte("dirty original before explicit restore\x00\xff")
			write(t, f.r.root, "a.bin", dirty)
			interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_RESTORE=1", "PEBBLE_TEST_WORKTREE_STAGE="+stage)
			later := []byte("concurrent later version must stay canonical")
			write(t, f.r.root, "a.bin", later)
			before := publicSnapshot(t, f.r.root)
			reopenRecovery(t, &f)
			receipt := automaticAbort(t, f.r, "heads/before")
			original := preservedVersion(t, receipt, "a.bin", "original", dirty)
			assertReconciledPublic(t, f.r, before, receipt)
			for i := 0; i < 2; i++ {
				reopenRecovery(t, &f)
				if err := f.r.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(filepath.Join(f.r.root, "a.bin"))
				if err != nil || !bytes.Equal(got, later) {
					t.Fatalf("later public version changed on repeat: %q %v", got, err)
				}
				if got, err = os.ReadFile(original); err != nil || !bytes.Equal(got, dirty) {
					t.Fatalf("dirty original changed on reopen: %q %v", got, err)
				}
			}
		})
	}
}

func TestAutomaticRecoveryVacantCanonicalAndLaterEdit(t *testing.T) {
	f := pauseCheckout(t, "retain-published")
	foreign := []byte("later independent public removal-file version")
	write(t, f.r.root, "removed", foreign)
	before := publicSnapshot(t, f.r.root)
	if _, exists := before["a.bin"]; exists {
		t.Fatal("fixture did not leave retained source vacant")
	}
	receipt := automaticAbort(t, f.r, "heads/before")
	backup := preservedVersion(t, receipt, "a.bin", "original", f.old["a.bin"])
	got, err := os.ReadFile(filepath.Join(f.r.root, "a.bin"))
	if err != nil || !bytes.Equal(got, f.old["a.bin"]) {
		t.Fatalf("vacant original was not automatically restored: %x %v", got, err)
	}
	assertReconciledPublic(t, f.r, before, receipt, "a.bin")
	publicInfo, err := os.Lstat(filepath.Join(f.r.root, "a.bin"))
	if err != nil {
		t.Fatal(err)
	}
	backupInfo, err := os.Lstat(backup)
	if err != nil || os.SameFile(publicInfo, backupInfo) {
		t.Fatalf("canonical restoration consumed/aliased the durable original: %v", err)
	}
	write(t, f.r.root, "a.bin", []byte("edit after automatic canonical restoration"))
	reopenRecovery(t, &f)
	if err = f.r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, err = os.ReadFile(backup); err != nil || !bytes.Equal(got, f.old["a.bin"]) {
		t.Fatalf("later canonical edit changed preserved original: %x %v", got, err)
	}
	if got, err = os.ReadFile(filepath.Join(f.r.root, "a.bin")); err != nil || string(got) != "edit after automatic canonical restoration" {
		t.Fatalf("repeated recovery overwrote later canonical edit: %q %v", got, err)
	}
}

func TestAutomaticRecoveryRestorationNoClobberRace(t *testing.T) {
	f := pauseCheckout(t, "retain-published")
	write(t, f.r.root, "removed", []byte("external conflict starts reconciliation"))
	performed := false
	f.r.fault = func(stage, path string) error {
		if stage == "repair-restore-rename" && !performed {
			performed = true
			return os.WriteFile(path, []byte("intervening canonical entry"), 0644)
		}
		return nil
	}
	receipt := automaticAbort(t, f.r, "heads/before")
	if !performed {
		t.Fatal("no-replace restoration syscall window was not reached")
	}
	if got, err := os.ReadFile(filepath.Join(f.r.root, "a.bin")); err != nil || string(got) != "intervening canonical entry" {
		t.Fatalf("intervening entry clobbered: %q %v", got, err)
	}
	preservedVersion(t, receipt, "a.bin", "original", f.old["a.bin"])
}

func TestAutomaticRecoveryBundleNoClobberRace(t *testing.T) {
	f := pauseCheckout(t, "install-published")
	write(t, f.r.root, "a.bin", []byte("concurrent canonical bytes"))
	occupied := ""
	f.r.fault = func(stage, path string) error {
		if stage == "repair-bundle-rename" && occupied == "" {
			occupied = path
			return os.WriteFile(path, []byte("foreign version owns the proposed archive name"), 0644)
		}
		return nil
	}
	if err := f.r.Recover(context.Background()); err == nil {
		t.Fatal("colliding bundle publication reported success")
	}
	if occupied == "" || !journalExists(t, f.r) {
		t.Fatal("collision fixture did not retain durable pending recovery")
	}
	public := publicSnapshot(t, f.r.root)
	occupiedID, err := filesystemIdentity(occupied)
	if err != nil {
		t.Fatal(err)
	}
	f.r.fault = nil
	reopenRecovery(t, &f)
	receipt := automaticAbort(t, f.r, "heads/before")
	if receipt.Directory == occupied {
		t.Fatal("automatic alternate placement reused foreign name")
	}
	assertReconciledPublic(t, f.r, public, receipt)
	preservedVersion(t, receipt, "a.bin", "original", f.old["a.bin"])
	if id, err := filesystemIdentity(occupied); err != nil || id != occupiedID {
		t.Fatalf("foreign archive entry replaced: %s %v", id, err)
	}
	if data, err := os.ReadFile(occupied); err != nil || string(data) != "foreign version owns the proposed archive name" {
		t.Fatalf("foreign archive bytes changed: %q %v", data, err)
	}
}

func TestAutomaticRecoveryReceiptLaterEdits(t *testing.T) {
	f := pauseCheckout(t, "install-published")
	write(t, f.r.root, "a.bin", []byte("canonical external bytes"))
	receipt := automaticAbort(t, f.r, "heads/before")
	location := preservedVersion(t, receipt, "a.bin", "original", f.old["a.bin"])
	write(t, filepath.Dir(location), filepath.Base(location), []byte("external edit to exposed recovery copy"))
	public := publicSnapshot(t, f.r.root)
	reopenRecovery(t, &f)
	if err := f.r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertPublicSnapshot(t, f.r.root, public)
	receipts, err := f.r.RecoveryReceipts()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, version := range receipts[0].Versions {
		if version.Location == location {
			found = version.Changed
		}
	}
	if !found {
		t.Fatal("receipt pretended a subsequently edited recovery version was unchanged")
	}
}

func TestAutomaticRecoveryEveryProcessPhase(t *testing.T) {
	phases := []string{"repair-plan", "repair-plan-published", "repair-restore", "repair-restore-anchored", "repair-restore-rename", "repair-restore-renamed", "repair-restore-barrier", "repair-restore-published", "repair-head", "repair-head-published", "repair-publish", "repair-bundle", "repair-bundle-anchored", "repair-bundle-rename", "repair-bundle-renamed", "repair-bundle-barrier", "repair-bundle-published"}
	for _, record := range []string{"repair-record-plan", "repair-record-location"} {
		for _, suffix := range []string{"", "-anchored", "-rename", "-renamed", "-barrier", "-published"} {
			phases = append(phases, record+suffix)
		}
	}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			f := pauseCheckout(t, "retain-published")
			foreign := []byte("external data survives every repair process exit")
			write(t, f.r.root, "removed", foreign)
			interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_RECOVER=1", "PEBBLE_TEST_WORKTREE_STAGE="+phase)
			reopenRecovery(t, &f)
			for i := 0; i < 2; i++ {
				err := f.r.Recover(context.Background())
				var aborted *RecoveryAbortedError
				if err != nil && !errors.As(err, &aborted) {
					t.Fatal(err)
				}
				reopenRecovery(t, &f)
			}
			if journalExists(t, f.r) {
				t.Fatal("repair did not converge after child termination")
			}
			receipts, err := f.r.RecoveryReceipts()
			if err != nil || len(receipts) != 1 {
				t.Fatalf("receipt after process exit: %#v %v", receipts, err)
			}
			preservedVersion(t, receipts[0], "a.bin", "original", f.old["a.bin"])
			if got, err := os.ReadFile(filepath.Join(f.r.root, "removed")); err != nil || !bytes.Equal(got, foreign) {
				t.Fatalf("external bytes changed: %q %v", got, err)
			}
			if head, err := f.r.GetHead(); err != nil || head != "heads/before" {
				t.Fatalf("aborted HEAD after process exit: %s %v", head, err)
			}
		})
	}
}

func TestAutomaticRecoveryBuiltCLI(t *testing.T) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	exe := filepath.Join(t.TempDir(), "pebble"+suffix)
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go"+suffix)
	build := exec.Command(goExe, "build", "-o", exe, "../../cmd/pebble")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual CLI: %v\n%s", err, out)
	}
	for _, retired := range []bool{false, true} {
		t.Run(fmt.Sprintf("archive-published-before-response=%t", retired), func(t *testing.T) {
			f := pauseCheckout(t, "install-published")
			foreign := []byte("later bytes retained by actual CLI recovery")
			write(t, f.r.root, "a.bin", foreign)
			if retired {
				// Real process exit after archive publication/intent retirement, before
				// any response. The independently built CLI below must rediscover it.
				interruptWorktree(t, f.r.root, "PEBBLE_TEST_WORKTREE_RECOVER=1", "PEBBLE_TEST_WORKTREE_STAGE=repair-bundle-published")
				if journalExists(t, f.r) {
					t.Fatal("publication-exit fixture did not retire active intent")
				}
			}
			public := publicSnapshot(t, f.r.root)
			for i := 0; i < 2; i++ {
				cmd := exec.Command(exe, "recover")
				cmd.Dir = f.r.root
				out, err := cmd.CombinedOutput()
				if err != nil || !bytes.Contains(out, []byte("Aborted checkout")) || !bytes.Contains(out, []byte("NOT completed")) || !bytes.Contains(out, []byte(filepath.Join(".pebble", "recovery", "archives"))) {
					t.Fatalf("fresh CLI recover %d: %v\n%s", i, err, out)
				}
				if bytes.Contains(out, []byte("Switched to branch")) {
					t.Fatal("CLI falsely reported requested target publication")
				}
				if !bytes.Contains(out, []byte(f.before.String())) || !bytes.Contains(out, []byte(f.after.String())) {
					t.Fatalf("CLI omitted durable original/requested IDs: %s", out)
				}
				receipts, e := f.r.RecoveryReceipts()
				if e != nil || len(receipts) != 1 {
					t.Fatalf("CLI receipt discovery: %#v %v", receipts, e)
				}
				for _, version := range receipts[0].Versions {
					if !bytes.Contains(out, []byte(version.Location)) {
						t.Fatalf("CLI omitted accessible preserved-version path %s: %s", version.Location, out)
					}
				}
				var restored []string
				if !retired {
					restored = []string{"removed"}
				}
				assertReconciledPublic(t, f.r, public, receipts[0], restored...)
			}
			reopenRecovery(t, &f)
			receipts, err := f.r.RecoveryReceipts()
			if err != nil || len(receipts) != 1 {
				t.Fatalf("CLI receipt: %#v %v", receipts, err)
			}
			preservedVersion(t, receipts[0], "a.bin", "original", f.old["a.bin"])
			if got, err := os.ReadFile(filepath.Join(f.r.root, "a.bin")); err != nil || !bytes.Equal(got, foreign) {
				t.Fatalf("CLI clobbered external bytes: %q %v", got, err)
			}
			for _, args := range [][]string{{"log"}, {"fsck"}} {
				cmd := exec.Command(exe, args...)
				cmd.Dir = f.r.root
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("actual CLI %s after abort: %v\n%s", fmt.Sprint(args), err, out)
				}
			}
		})
	}
}

func TestAutomaticRecoveryRejectsEscapingReceipt(t *testing.T) {
	f := pauseCheckout(t, "install-published")
	write(t, f.r.root, "a.bin", []byte("later canonical bytes"))
	receipt := automaticAbort(t, f.r, "heads/before")
	public := publicSnapshot(t, f.r.root)
	registry, err := f.r.recoveryRegistry(filepath.Base(receipt.Directory), false)
	if err != nil {
		t.Fatal(err)
	}
	var location recoveryLocation
	if err = readRecoveryRecord(registry, "000000.json", &location); err != nil {
		t.Fatal(err)
	}
	location.Plan.Versions[0].Slot = "../outside-archive"
	location.PlanSHA256, err = recoveryDigest(location.Plan)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeWorktreeRecord(location, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(registry, "000000.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		reopenRecovery(t, &f)
		if _, err = f.r.RecoveryReceipts(); err == nil {
			t.Fatal("malformed receipt exposed an escaping preserved-version path")
		}
		if err = f.r.Recover(context.Background()); err == nil {
			t.Fatal("recovery reported success with invalid durable ownership")
		}
		assertPublicSnapshot(t, f.r.root, public)
	}
}
