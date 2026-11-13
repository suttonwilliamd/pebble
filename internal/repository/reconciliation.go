package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

// RecoveryAbortedError distinguishes safe reconciliation from completion of the
// requested checkout/restore. Recovery itself has finished; the requested tree
// was NOT published. The durable receipt remains discoverable after reopening.
type RecoveryAbortedError struct {
	Operation string
	Directory string
}

func (e *RecoveryAbortedError) Error() string {
	return fmt.Sprintf("%s aborted and automatically reconciled; preserved versions: %s; requested operation was not completed", e.Operation, e.Directory)
}

type recoveryVersion struct {
	Path      string
	Role      string
	Slot      string
	Image     *fileImage
	Directory string
}

type recoveryRestore struct {
	Path        string
	Slot        string
	VersionSlot string
	Image       *fileImage
	Directory   string
}

type recoveryPlan struct {
	Transaction     string
	RootIdentity    string
	JournalIdentity string
	IntentSHA256    string
	Operation       string
	BeforeHead      string
	RequestedHead   string
	BeforeID        storev2.ID
	RequestedID     storev2.ID
	Reason          string
	Versions        []recoveryVersion
	Restores        []recoveryRestore
}

type recoveryLocation struct {
	Plan        recoveryPlan
	PlanSHA256  string
	Destination string
}

// RecoveryVersion identifies a preserved physical version. Changed means a user
// subsequently edited/replaced/removed it; recovery never overwrites that edit.
type RecoveryVersion struct {
	Path     string
	Role     string
	Location string
	Changed  bool
}

// RecoveryReceipt is durable, discoverable evidence of an aborted operation,
// not an assertion that the requested working tree was successfully installed.
type RecoveryReceipt struct {
	Operation     string
	Directory     string
	BeforeHead    string
	RequestedHead string
	BeforeID      storev2.ID
	RequestedID   storev2.ID
	Reason        string
	Restorations  []RecoveryRestoration
	Versions      []RecoveryVersion
}

type RecoveryRestoration struct {
	Path      string
	Location  string
	Canonical bool
	Changed   bool
}

func recoverySlot(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`) && filepath.Base(name) == name
}

func recoveryDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func encodeWorktreeRecord(value any, version int) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	encoded, err := json.Marshal(journalEnvelope{version, data, hex.EncodeToString(sum[:])})
	if err == nil && len(encoded) > maxJournalBytes {
		err = fmt.Errorf("worktree record exceeds %d bytes", maxJournalBytes)
	}
	return encoded, err
}

func readRecoveryRecord(dir, name string, value any) error {
	var data []byte
	err := (&Repository{root: dir}).readFile(name, func(f *os.File) error {
		var e error
		data, e = io.ReadAll(io.LimitReader(f, maxJournalBytes+1))
		return e
	})
	if err != nil {
		return err
	}
	if len(data) > maxJournalBytes {
		return fmt.Errorf("oversized recovery record")
	}
	var envelope journalEnvelope
	if err = strictJSON(data, &envelope); err != nil {
		return err
	}
	sum := sha256.Sum256(envelope.Intent)
	if envelope.Version != 3 || envelope.SHA256 != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("unsupported or corrupt reconciliation record")
	}
	return strictJSON(envelope.Intent, value)
}

func (r *Repository) checkRecoveryRoot(plan *worktreeIntent) error {
	id, err := filesystemIdentity(r.root)
	if err != nil || id != plan.RootIdentity {
		return errors.Join(fmt.Errorf("recovery working root is inaccessible or replaced"), err)
	}
	return nil
}

func (r *Repository) checkRecoveryHistory(plan *worktreeIntent) error {
	if err := r.checkRecoveryRoot(plan); err != nil {
		return err
	}
	return r.inspectHistory(plan)
}

func (r *Repository) writeRecoveryRecord(ctx context.Context, dir, name, phase string, value any, check func() error) (err error) {
	data, err := encodeWorktreeRecord(value, 3)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "record-stage-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	image, err := inspectImage(&Repository{root: dir}, filepath.Base(f.Name()))
	if err != nil {
		return err
	}
	return r.transactionMove(ctx, phase, f.Name(), filepath.Join(dir, name), image, false, check)
}

func (r *Repository) prepareRecovery(ctx context.Context, plan *worktreeIntent, reason error) (*recoveryPlan, error) {
	if err := r.checkRecoveryHistory(plan); err != nil {
		return nil, err
	}
	if plan.version != 3 {
		if err := r.upgradeRecoveryIntent(ctx, plan); err != nil {
			return nil, err
		}
	}
	if err := r.checkRecoverySources(plan); err != nil {
		return nil, err
	}
	stage := r.journalPath()
	journal, err := filesystemIdentity(stage)
	if err != nil {
		return nil, err
	}
	digest, err := recoveryDigest(plan)
	if err != nil {
		return nil, err
	}
	repair := &recoveryPlan{Transaction: plan.RecoveryID, RootIdentity: plan.RootIdentity, JournalIdentity: journal, IntentSHA256: digest, Operation: plan.Kind, BeforeHead: plan.BeforeHead, RequestedHead: plan.AfterHead, BeforeID: plan.BeforeID, RequestedID: plan.AfterID, Reason: reason.Error()}
	if strings.HasPrefix(plan.RecoveryID, "worktree-stage-legacy-") {
		legacy, err := r.legacyRecoveryIntent(plan)
		if err != nil || !legacy {
			return nil, errors.Join(fmt.Errorf("legacy abort checkpoint is missing or invalid"), err)
		}
		image, err := inspectImage(&Repository{root: stage}, "legacy-intent.json")
		if err != nil {
			return nil, err
		}
		repair.Versions = append(repair.Versions, recoveryVersion{Path: ".pebble/worktree-journal/intent.json", Role: "legacy-intent", Slot: "legacy-intent.json", Image: image})
	}
	if err = r.prepareRecoveryDirectories(ctx, plan, repair); err != nil {
		return nil, err
	}
	for i := range plan.Files {
		if err = r.prepareRecoveryFile(ctx, plan, repair, i); err != nil {
			return nil, err
		}
	}
	if err = r.boundary(ctx, "repair-plan", stage); err != nil {
		return nil, err
	}
	if err = r.writeRecoveryRecord(ctx, stage, "repair.json", "repair-record-plan", repair, func() error { return r.checkRecoveryHistory(plan) }); err != nil {
		return nil, err
	}
	if err = r.boundary(ctx, "repair-plan-published", stage); err != nil {
		return nil, err
	}
	return repair, nil
}

func (r *Repository) checkRecoveryPlan(plan *worktreeIntent, repair *recoveryPlan) error {
	if err := r.checkRecoveryHistory(plan); err != nil {
		return err
	}
	digest, err := recoveryDigest(plan)
	if err != nil {
		return err
	}
	id, err := filesystemIdentity(r.journalPath())
	if err != nil {
		return err
	}
	if repair.Transaction != plan.RecoveryID || repair.RootIdentity != plan.RootIdentity || repair.JournalIdentity != id || repair.IntentSHA256 != digest || repair.Operation != plan.Kind || repair.BeforeHead != plan.BeforeHead || repair.RequestedHead != plan.AfterHead || repair.BeforeID != plan.BeforeID || repair.RequestedID != plan.AfterID {
		return fmt.Errorf("reconciliation belongs to another intent or directory")
	}
	private := &Repository{root: r.journalPath()}
	if err := validateRecoveryRestores(plan, repair); err != nil {
		return err
	}
	for _, version := range repair.Versions {
		if !recoverySlot(version.Slot) {
			return fmt.Errorf("unsafe preserved-version slot")
		}
		if version.Image != nil {
			image, err := inspectRecoveryImage(private, version.Slot)
			if err != nil || !equalImage(image, version.Image) {
				return errors.Join(fmt.Errorf("preserved version changed: %s", version.Slot), err)
			}
		} else {
			id, err := filesystemIdentity(filepath.Join(private.root, version.Slot))
			if err != nil || id != version.Directory {
				return errors.Join(fmt.Errorf("preserved directory changed: %s", version.Slot), err)
			}
		}
	}
	return nil
}

// Missing, aliased or rebound parents never authorize creating a tree underneath
// a different owner. The first-class metadata archive is the alternate path.
func (r *Repository) canRestoreOriginal(plan *worktreeIntent, name string) (string, bool, error) {
	for parent := filepath.ToSlash(filepath.Dir(name)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
		found := false
		for _, dir := range plan.Directories {
			if dir.Path != parent || dir.Staged {
				continue
			}
			id, e := filesystemIdentity(filepath.Join(r.root, filepath.FromSlash(parent)))
			if errors.Is(e, os.ErrNotExist) || e == nil && id != dir.Identity {
				return "", false, nil
			}
			if e != nil {
				return "", false, e
			}
			found = true
			break
		}
		if !found {
			return "", false, nil
		}
	}
	p, err := r.safePath(name)
	if errors.Is(err, errPathBlocked) || errors.Is(err, errPathAlias) || errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if _, err = os.Lstat(p); err == nil {
		return "", false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	return p, true, nil
}

func (r *Repository) recoveryRegistry(transaction string, create bool) (string, error) {
	root := filepath.Join(r.root, ".pebble", "recovery")
	base := filepath.Join(root, "receipts")
	for _, path := range []string{root, filepath.Join(root, "archives"), base, filepath.Join(base, transaction)} {
		if create {
			if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("recovery registry is not an ordinary directory: %s", path)
		}
	}
	return filepath.Join(base, transaction), nil
}

func readRecoveryLocations(dir string) ([]recoveryLocation, error) {
	entries, err := readDirectory(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "record-stage-") {
			continue
		}
		if len(name) != len("000000.json") || !strings.HasSuffix(name, ".json") {
			return nil, fmt.Errorf("unexpected recovery registry entry: %s", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	locations := make([]recoveryLocation, 0, len(names))
	for i, name := range names {
		if name != fmt.Sprintf("%06d.json", i) {
			return nil, fmt.Errorf("incomplete recovery location history")
		}
		var location recoveryLocation
		if err = readRecoveryRecord(dir, name, &location); err != nil {
			return nil, err
		}
		digest, e := recoveryDigest(location.Plan)
		if e != nil || digest != location.PlanSHA256 || !strings.HasPrefix(location.Destination, "worktree-stage-") || strings.ContainsAny(location.Destination, `/\`) {
			return nil, errors.Join(fmt.Errorf("invalid recovery location receipt"), e)
		}
		locations = append(locations, location)
	}
	return locations, nil
}

func (r *Repository) recoveryLocation(ctx context.Context, plan *worktreeIntent, repair *recoveryPlan) (*recoveryLocation, error) {
	dir, err := r.recoveryRegistry(repair.Transaction, true)
	if err != nil {
		return nil, err
	}
	locations, err := readRecoveryLocations(dir)
	if err != nil {
		return nil, err
	}
	digest, err := recoveryDigest(repair)
	if err != nil {
		return nil, err
	}
	destination := repair.Transaction
	if len(locations) != 0 {
		last := &locations[len(locations)-1]
		if last.PlanSHA256 != digest {
			return nil, fmt.Errorf("recovery location belongs to another plan")
		}
		if _, err = os.Lstat(filepath.Join(r.root, ".pebble", "recovery", "archives", last.Destination)); errors.Is(err, os.ErrNotExist) {
			return last, nil
		} else if err != nil {
			return nil, err
		}
		// A user owns that name now. Choose a fresh durable candidate, never
		// move the intervening entry or ask the operator to rename it.
		destination += fmt.Sprintf("-%06d", len(locations))
	}
	location := &recoveryLocation{Plan: *repair, PlanSHA256: digest, Destination: destination}
	if err = r.writeRecoveryRecord(ctx, dir, fmt.Sprintf("%06d.json", len(locations)), "repair-record-location", location, func() error { return r.checkRecoveryPlan(plan, repair) }); err != nil {
		return nil, err
	}
	for path := dir; ; path = filepath.Dir(path) {
		if err = syncMigrationDirectory(path); err != nil {
			return nil, err
		}
		if path == filepath.Join(r.root, ".pebble") {
			break
		}
	}
	return location, nil
}

func (r *Repository) resumeRecovery(ctx context.Context, plan *worktreeIntent, repair *recoveryPlan) error {
	check := func() error { return r.checkRecoveryPlan(plan, repair) }
	if err := check(); err != nil {
		return err
	}
	private := &Repository{root: r.journalPath()}
	for _, restore := range repair.Restores {
		source, err := captureRecoveryVersion(private, restore.Slot, restore.Path, "")
		if err != nil {
			return err
		}
		if source == nil {
			// Already restored. Never reuse an old intent to erase a later edit.
			continue
		}
		if restore.Image != nil && !equalImage(source.Image, restore.Image) || restore.Directory != "" && source.Directory != restore.Directory {
			return fmt.Errorf("restoration source ownership changed: %s", restore.Path)
		}
		target, available, err := r.canRestoreOriginal(plan, restore.Path)
		if err != nil {
			return fmt.Errorf("automatic public-name repair pending for %s: %w", restore.Path, err)
		}
		if !available {
			continue
		}
		owned := func() error {
			if err := check(); err != nil {
				return err
			}
			_, available, err := r.canRestoreOriginal(plan, restore.Path)
			if err != nil {
				return err
			}
			if !available {
				return errRecoveryDestinationChanged
			}
			if restore.Directory != "" {
				id, err := filesystemIdentity(filepath.Join(private.root, restore.Slot))
				if err != nil {
					return err
				}
				if id != restore.Directory {
					return fmt.Errorf("restoration directory ownership changed")
				}
			}
			return nil
		}
		if err = r.transactionMove(ctx, "repair-restore", filepath.Join(private.root, restore.Slot), target, restore.Image, false, owned); err != nil {
			if errors.Is(err, os.ErrExist) || errors.Is(err, errRecoveryDestinationChanged) {
				continue
			}
			return fmt.Errorf("automatic public-name repair pending for %s: %w", restore.Path, err)
		}
	}
	if err := check(); err != nil {
		return err
	}
	if err := r.boundary(ctx, "repair-head", plan.BeforeHead); err != nil {
		return err
	}
	head, err := r.GetHead()
	if err != nil {
		return err
	}
	if head != plan.BeforeHead && head != plan.AfterHead {
		return fmt.Errorf("HEAD changed outside automatic reconciliation")
	}
	if err = r.updateIntentHead(ctx, head, plan.BeforeHead, plan); err != nil {
		return err
	}
	if err = r.boundary(ctx, "repair-head-published", plan.BeforeHead); err != nil {
		return err
	}
	location, err := r.recoveryLocation(ctx, plan, repair)
	if err != nil {
		return err
	}
	if err = syncRecoveryTree(private.root); err != nil {
		return err
	}
	if err = r.boundary(ctx, "repair-publish", location.Destination); err != nil {
		return err
	}
	destination := filepath.Join(r.root, ".pebble", "recovery", "archives", location.Destination)
	if err = r.transactionMove(ctx, "repair-bundle", private.root, destination, nil, false, check); err != nil {
		return fmt.Errorf("automatic reconciliation pending; recovery archive publication failed: %w", err)
	}
	return &RecoveryAbortedError{Operation: plan.Kind, Directory: destination}
}

// RecoveryReceipts is read-only. Reopening never resumes an intent implicitly.
func (r *Repository) RecoveryReceipts() ([]RecoveryReceipt, error) {
	base := filepath.Join(r.root, ".pebble", "recovery", "receipts")
	entries, err := readDirectory(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var receipts []RecoveryReceipt
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "worktree-stage-") || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("invalid recovery registry transaction")
		}
		dir, err := r.recoveryRegistry(name, false)
		if err != nil {
			return nil, err
		}
		locations, err := readRecoveryLocations(dir)
		if err != nil {
			return nil, err
		}
		if len(locations) == 0 {
			continue
		}
		location := locations[len(locations)-1]
		root, err := filesystemIdentity(r.root)
		if err != nil || root != location.Plan.RootIdentity {
			return nil, errors.Join(fmt.Errorf("recovery receipt belongs to another root"), err)
		}
		bundle := filepath.Join(r.root, ".pebble", "recovery", "archives", location.Destination)
		id, err := filesystemIdentity(bundle)
		if errors.Is(err, os.ErrNotExist) {
			// A location may have been durably reserved before interruption.
			if active, e := filesystemIdentity(r.journalPath()); e == nil && active == location.Plan.JournalIdentity {
				continue
			}
		}
		if err != nil || id != location.Plan.JournalIdentity {
			return nil, errors.Join(fmt.Errorf("published recovery path is missing or replaced: %s", bundle), err)
		}
		var sealed recoveryPlan
		if err = readRecoveryRecord(bundle, "repair.json", &sealed); err != nil {
			return nil, err
		}
		digest, err := recoveryDigest(&sealed)
		if err != nil || digest != location.PlanSHA256 {
			return nil, errors.Join(fmt.Errorf("published receipt does not match its archived repair plan"), err)
		}
		receipt := RecoveryReceipt{Operation: location.Plan.Operation, Directory: bundle, BeforeHead: location.Plan.BeforeHead, RequestedHead: location.Plan.RequestedHead, BeforeID: location.Plan.BeforeID, RequestedID: location.Plan.RequestedID, Reason: location.Plan.Reason}
		for _, version := range location.Plan.Versions {
			if !recoverySlot(version.Slot) || (version.Image == nil) == (version.Directory == "") {
				return nil, fmt.Errorf("invalid published preserved-version ownership")
			}
			changed := false
			if version.Image != nil {
				image, e := inspectRecoveryImage(&Repository{root: bundle}, version.Slot)
				changed = e != nil || !equalImage(image, version.Image)
			} else {
				id, e := filesystemIdentity(filepath.Join(bundle, version.Slot))
				changed = e != nil || id != version.Directory
			}
			receipt.Versions = append(receipt.Versions, RecoveryVersion{Path: version.Path, Role: version.Role, Location: filepath.Join(bundle, version.Slot), Changed: changed})
		}
		for _, restore := range location.Plan.Restores {
			if !recoverySlot(restore.Slot) || !recoverySlot(restore.VersionSlot) || validPath(restore.Path) != nil || (restore.Image == nil) == (restore.Directory == "") {
				return nil, fmt.Errorf("invalid published restoration ownership")
			}
			receipt.Restorations = append(receipt.Restorations, r.restorationStatus(bundle, restore))
		}
		receipts = append(receipts, receipt)
	}
	return receipts, nil
}

func (r *Repository) finishPublishedRecoveries() error {
	receipts, err := r.RecoveryReceipts()
	if err != nil {
		return err
	}
	if len(receipts) == 0 {
		return nil
	}
	// A child may have exited after the journal rename but before its parent
	// barriers. Do not acknowledge completion until both namespace barriers pass.
	return errors.Join(syncMigrationDirectory(filepath.Join(r.root, ".pebble")), syncMigrationDirectory(filepath.Join(r.root, ".pebble", "recovery")), syncMigrationDirectory(filepath.Join(r.root, ".pebble", "recovery", "archives")))
}
