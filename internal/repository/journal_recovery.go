package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type journalMove struct {
	IntentSHA256 string
	Stage        string
	Index        int
	Identity     string
}

func moveRecordName(stage string, index int) string {
	return fmt.Sprintf("move-%s-%06d.json", stage, index)
}

func (r *Repository) recordedMove(plan *worktreeIntent, stage string, index int, identity string) (bool, error) {
	var move journalMove
	err := readRecoveryRecord(r.journalPath(), moveRecordName(stage, index), &move)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	digest, err := recoveryDigest(plan)
	if err != nil {
		return false, err
	}
	if move.IntentSHA256 != digest || move.Stage != stage || move.Index != index || move.Identity != identity {
		return false, fmt.Errorf("move ticket belongs to another intent or source")
	}
	return true, nil
}

func (r *Repository) recordMove(ctx context.Context, plan *worktreeIntent, stage string, index int, identity string) error {
	// Genuine legacy recovery keeps its original protocol. New semantics are
	// introduced only by the guarded explicit recovery upgrade below.
	if plan.version < 3 {
		return nil
	}
	exists, err := r.recordedMove(plan, stage, index, identity)
	if err != nil || exists {
		return err
	}
	digest, err := recoveryDigest(plan)
	if err != nil {
		return err
	}
	move := journalMove{digest, stage, index, identity}
	// The ticket only permits conservative abort if a staged source was
	// consumed. Public moves still validate the complete history/worktree,
	// and HEAD publication validates both graph pins under the store guard.
	return r.writeRecoveryRecord(ctx, r.journalPath(), moveRecordName(stage, index), "repair-record-move", &move, func() error { return r.checkRecoveryRoot(plan) })
}

func (r *Repository) checkRecoverySources(plan *worktreeIntent) error {
	private := &Repository{root: r.journalPath()}
	legacyChecked, legacy := false, false
	allowLegacyAbort := func() (bool, error) {
		if legacyChecked {
			return legacy, nil
		}
		var err error
		legacy, err = r.legacyRecoveryIntent(plan)
		legacyChecked = err == nil
		return legacy, err
	}
	for i, file := range plan.Files {
		if file.Target == nil || file.Keep {
			continue
		}
		staged, err := inspectRecoveryImage(private, stageName(i))
		if err != nil {
			return err
		}
		if equalImage(staged, file.Target) {
			continue
		}
		live, liveErr := r.inspectLiveImage(plan, file.Path)
		if liveErr == nil && equalImage(live, file.Target) {
			continue
		}
		moved, err := r.recordedMove(plan, "install", i, file.Target.Identity)
		if err != nil {
			return err
		}
		if !moved {
			legacy, err := allowLegacyAbort()
			if err != nil {
				return err
			}
			if !legacy {
				return fmt.Errorf("missing or changed unconsumed target recovery material: %s", file.Path)
			}
		}
	}
	for i, dir := range plan.Directories {
		if !dir.Staged {
			continue
		}
		if p, err := private.directoryAt(directoryName(i)); err != nil {
			return err
		} else if p != "" {
			id, err := filesystemIdentity(p)
			if err != nil || id != dir.Identity {
				return errors.Join(fmt.Errorf("staged directory ownership changed: %s", dir.Path), err)
			}
			continue
		}
		if p, err := r.directoryAt(dir.Path); err == nil && p != "" {
			if id, err := filesystemIdentity(p); err == nil && id == dir.Identity {
				continue
			}
		}
		moved, err := r.recordedMove(plan, "directory", i, dir.Identity)
		if err != nil {
			return err
		}
		if !moved {
			legacy, err := allowLegacyAbort()
			if err != nil {
				return err
			}
			if !legacy {
				return fmt.Errorf("missing unconsumed directory recovery material: %s", dir.Path)
			}
		}
	}
	return nil
}

func (r *Repository) upgradeRecoveryIntent(ctx context.Context, plan *worktreeIntent) error {
	stage := r.journalPath()
	private := &Repository{root: stage}
	// Preserve the exact old checksum envelope before the explicit local-only
	// upgrade. The archive exposes it; no legacy recovery evidence is discarded.
	original, err := inspectImage(private, "intent.json")
	if err != nil {
		return err
	}
	saved, err := inspectImage(private, "legacy-intent.json")
	if err != nil {
		return err
	}
	if saved == nil {
		copy, err := private.copyImage(ctx, "intent.json", stage, original)
		if err != nil {
			return err
		}
		if err = renameMigrationExclusive(copy, filepath.Join(stage, "legacy-intent.json")); err != nil {
			return err
		}
		saved, err = inspectImage(private, "legacy-intent.json")
		if err != nil {
			return err
		}
	}
	if !sameImageContent(original, saved) {
		return fmt.Errorf("preserved legacy intent does not match pending operation")
	}
	if err = syncMigrationDirectory(stage); err != nil {
		return err
	}
	if err = r.boundary(ctx, "repair-upgrade-preserved", stage); err != nil {
		return err
	}
	for i := range plan.Files {
		file := &plan.Files[i]
		if file.Original == nil || file.Keep {
			continue
		}
		backup, err := inspectImage(private, preimageName(i))
		if err != nil {
			return err
		}
		if backup == nil {
			owner, name := private, retainedName(i)
			retained, err := captureRecoveryVersion(private, name, file.Path, "")
			if err != nil {
				return err
			}
			var image *fileImage
			if retained != nil {
				image = retained.Image
			}
			if !equalImage(image, file.Original) {
				owner, name = r, file.Path
				image, err = r.inspectLiveImage(plan, name)
			}
			var copy string
			if equalImage(image, file.Original) {
				copy, err = owner.copyImage(ctx, name, stage, image)
			} else {
				copy, err = r.stageLegacyOriginal(ctx, plan, *file, stage)
				if err == nil && copy == "" {
					return fmt.Errorf("legacy intent has no available original preimage for %s; remaining versions preserved, missing bytes cannot be fabricated", file.Path)
				}
			}
			if err != nil {
				return err
			}
			if err = renameMigrationExclusive(copy, filepath.Join(stage, preimageName(i))); err != nil {
				return err
			}
			backup, err = inspectImage(private, preimageName(i))
			if err != nil {
				return err
			}
		}
		if !sameImageContent(backup, file.Original) {
			return fmt.Errorf("legacy upgrade preimage changed: %s", file.Path)
		}
		file.Backup = backup
	}
	id, err := filesystemIdentity(stage)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(id))
	plan.RecoveryID = "worktree-stage-legacy-" + hex.EncodeToString(sum[:12])
	plan.version = 3
	if err = r.validateIntent(plan); err != nil {
		return err
	}
	encoded, err := encodeWorktreeRecord(plan, 3)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(stage, "upgrade-stage-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(encoded)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = r.boundary(ctx, "repair-upgrade", stage); err != nil {
		return err
	}
	if err = r.checkRecoveryHistory(plan); err != nil {
		return err
	}
	// Only the local envelope is replaced. Exact legacy intent bytes remain in
	// legacy-intent.json; immutable objects, IDs and history are untouched.
	if err = replaceRecoveryIntent(f.Name(), filepath.Join(stage, "intent.json")); err != nil {
		return err
	}
	return r.boundary(ctx, "repair-upgrade-published", stage)
}

// Reconstruct only an exact recorded preimage from immutable pinned history.
// This does not invent dirty bytes or rewrite any preserved object or ID.
func (r *Repository) stageLegacyOriginal(ctx context.Context, plan *worktreeIntent, file journalFile, dir string) (string, error) {
	entries, err := r.tree(plan.BeforeID)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.Path != file.Path || entry.Symlink != file.Original.Symlink {
			continue
		}
		size, sum, err := r.store.FileInfo(ctx, entry.Manifest)
		if err != nil {
			return "", err
		}
		if size != file.Original.Size || sum != file.Original.Sum {
			return "", nil
		}
		// The backup is the recorded native preimage, not a new tree entry;
		// unobservable historical metadata remains unchanged in the store.
		entry.Executable = file.Original.Executable
		entry.DirectoryLink = file.Original.DirectoryLink
		return r.stageFileContext(ctx, dir, entry)
	}
	return "", nil
}

// Old replay did not write move tickets. A missing old staged target cannot be
// distinguished from an installed target subsequently removed by an editor.
// This evidence authorizes only safe abort with independently backed originals,
// never successful target publication or relaxation of new v3 source checks.
func (r *Repository) legacyRecoveryIntent(plan *worktreeIntent) (bool, error) {
	var data []byte
	private := &Repository{root: r.journalPath()}
	err := private.readFile("legacy-intent.json", func(f *os.File) error {
		var err error
		data, err = io.ReadAll(io.LimitReader(f, maxJournalBytes+1))
		return err
	})
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var envelope journalEnvelope
	if len(data) > maxJournalBytes {
		return false, fmt.Errorf("oversized preserved legacy intent")
	}
	if err = strictJSON(data, &envelope); err != nil {
		return false, err
	}
	sum := sha256.Sum256(envelope.Intent)
	if envelope.Version < 1 || envelope.Version > 2 || envelope.SHA256 != hex.EncodeToString(sum[:]) {
		return false, fmt.Errorf("unsupported or corrupt preserved legacy intent")
	}
	var old worktreeIntent
	if err = strictJSON(envelope.Intent, &old); err != nil {
		return false, err
	}
	old.version = envelope.Version
	if err = r.validateIntent(&old); err != nil {
		return false, err
	}
	current := *plan
	current.RecoveryID = ""
	current.Files = append([]journalFile(nil), plan.Files...)
	for i := range current.Files {
		if current.Files[i].Original != nil && !current.Files[i].Keep && current.Files[i].Backup == nil {
			return false, fmt.Errorf("legacy abort lacks an independently preserved original")
		}
		current.Files[i].Backup = nil
	}
	got, err := recoveryDigest(&current)
	if err != nil {
		return false, err
	}
	want, err := recoveryDigest(&old)
	if err != nil || got != want {
		return false, errors.Join(fmt.Errorf("preserved legacy intent belongs to another operation"), err)
	}
	return true, nil
}

func syncRecoveryTree(path string) error {
	children, err := readDirectory(path)
	if err != nil {
		return err
	}
	for _, child := range children {
		p := filepath.Join(path, child.Name())
		if child.IsDir() && child.Type()&os.ModeSymlink == 0 {
			if err = syncRecoveryTree(p); err != nil {
				return err
			}
		} else {
			if !child.Type().IsRegular() && child.Type()&os.ModeSymlink == 0 {
				return fmt.Errorf("unsupported preserved filesystem object: %s", p)
			}
			if err = syncOriginal(p); err != nil {
				return err
			}
		}
	}
	return syncMigrationDirectory(path)
}
