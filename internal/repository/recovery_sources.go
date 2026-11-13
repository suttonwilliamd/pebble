package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Existing native links may have a literal spelling that cannot be imported
// into a portable preserved tree. Recovery does not import or dereference them:
// it preserves their native literal identity in the same filesystem namespace.
func inspectRecoveryImage(r *Repository, name string) (*fileImage, error) {
	p, err := r.safePath(name)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink == 0 {
		return inspectImage(r, name)
	}
	id, err := filesystemIdentity(p)
	if err != nil {
		return nil, err
	}
	a, err := anchorPath(p, false)
	if err != nil {
		return nil, err
	}
	link, readErr := a.readLink()
	if err = errors.Join(readErr, a.close()); err != nil {
		return nil, err
	}
	if err = validLinkTarget(link.target); err != nil {
		return nil, err
	}
	after, err := os.Lstat(p)
	if err != nil || !sameWorkingInfo(before, after) {
		return nil, errors.Join(fmt.Errorf("native recovery link changed: %s", name), err)
	}
	finalID, err := filesystemIdentity(p)
	if err != nil || finalID != id {
		return nil, errors.Join(fmt.Errorf("native recovery link identity changed: %s", name), err)
	}
	return &fileImage{Identity: id, Size: uint64(len(link.target)), Sum: sha256.Sum256([]byte(link.target)), Symlink: true, DirectoryLink: link.directory, Modified: before.ModTime().UnixNano()}, nil
}

func captureRecoveryVersion(r *Repository, slot, path, role string) (*recoveryVersion, error) {
	p, err := r.safePath(slot)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	version := &recoveryVersion{Path: path, Role: role, Slot: slot}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		id, err := filesystemIdentity(p)
		if err != nil {
			return nil, err
		}
		// The no-follow directory open also rejects a last-moment link swap.
		if _, err = readDirectory(p); err != nil {
			return nil, err
		}
		version.Directory = id
	} else {
		version.Image, err = inspectRecoveryImage(r, slot)
		if err != nil {
			return nil, err
		}
	}
	return version, nil
}

func copyRecoveryDirectory(ctx context.Context, source, parent string) (_ string, err error) {
	before, err := os.Lstat(source)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return "", errors.Join(fmt.Errorf("recovery copy requires an ordinary directory: %s", source), err)
	}
	copy, err := os.MkdirTemp(parent, "directory-copy-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(copy)
		}
	}()
	children, err := readDirectory(source)
	if err != nil {
		return "", err
	}
	private := &Repository{root: source}
	for _, child := range children {
		if err = ctx.Err(); err != nil {
			return "", err
		}
		var staged string
		if child.IsDir() && child.Type()&os.ModeSymlink == 0 {
			staged, err = copyRecoveryDirectory(ctx, filepath.Join(source, child.Name()), copy)
		} else {
			image, e := inspectRecoveryImage(private, child.Name())
			if e != nil {
				return "", e
			}
			staged, err = private.copyImage(ctx, child.Name(), copy, image)
		}
		if err != nil {
			return "", err
		}
		if err = renameMigrationExclusive(staged, filepath.Join(copy, child.Name())); err != nil {
			return "", err
		}
	}
	after, err := os.Lstat(source)
	if err != nil || !sameWorkingInfo(before, after) {
		return "", errors.Join(fmt.Errorf("recovery source directory changed during copy: %s", source), err)
	}
	if err = os.Chmod(copy, before.Mode().Perm()); err != nil {
		return "", err
	}
	if err = syncRecoveryTree(copy); err != nil {
		return "", err
	}
	return copy, nil
}

func (r *Repository) saveRecoverySource(ctx context.Context, repair *recoveryPlan, version *recoveryVersion, savedSlot string) (*recoveryRestore, error) {
	private := &Repository{root: r.journalPath()}
	saved, err := captureRecoveryVersion(private, savedSlot, version.Path, version.Role)
	if err != nil {
		return nil, err
	}
	if saved == nil {
		var copy string
		if version.Image != nil {
			copy, err = private.copyImage(ctx, version.Slot, private.root, version.Image)
		} else {
			copy, err = copyRecoveryDirectory(ctx, filepath.Join(private.root, version.Slot), private.root)
		}
		if err != nil {
			return nil, err
		}
		if err = renameMigrationExclusive(copy, filepath.Join(private.root, savedSlot)); err != nil {
			return nil, err
		}
		saved, err = captureRecoveryVersion(private, savedSlot, version.Path, version.Role)
		if err != nil {
			return nil, err
		}
	}
	if version.Image != nil && !sameImageContent(saved.Image, version.Image) || version.Directory != "" && saved.Directory == "" {
		return nil, fmt.Errorf("saved displaced version changed: %s", version.Path)
	}
	repair.Versions = append(repair.Versions, *saved)
	return &recoveryRestore{Path: version.Path, Slot: version.Slot, VersionSlot: savedSlot, Image: version.Image, Directory: version.Directory}, nil
}

func (r *Repository) prepareRecoveryFile(ctx context.Context, plan *worktreeIntent, repair *recoveryPlan, i int) error {
	file := plan.Files[i]
	if file.Keep {
		// Pebble never moved this public entry. A later external deletion must
		// remain a deletion; unchanged content is already in pinned history.
		return nil
	}
	private := &Repository{root: r.journalPath()}
	if file.Backup != nil {
		backup, err := inspectRecoveryImage(private, preimageName(i))
		if err != nil || !equalImage(backup, file.Backup) {
			return errors.Join(fmt.Errorf("original preimage missing or changed: %s", file.Path), err)
		}
		repair.Versions = append(repair.Versions, recoveryVersion{Path: file.Path, Role: "original", Slot: preimageName(i), Image: backup})
	}
	retained, err := captureRecoveryVersion(private, retainedName(i), file.Path, "retained-original")
	if err != nil {
		return err
	}
	if retained != nil && !equalImage(retained.Image, file.Original) {
		retained.Role = "displaced"
	}
	staged, err := captureRecoveryVersion(private, stageName(i), file.Path, "requested")
	if err != nil {
		return err
	}
	if staged != nil && !equalImage(staged.Image, file.Target) {
		staged.Role = "displaced"
	}
	// A modified installed target reversed into its private source is the
	// newest displaced public version. Prefer it over the old tracked image.
	chosen := retained
	if staged != nil && staged.Role == "displaced" {
		chosen = staged
	}
	_, available, err := r.canPlanRestore(plan, repair, file.Path)
	if err != nil {
		return err
	}
	if available && chosen != nil {
		restore, err := r.saveRecoverySource(ctx, repair, chosen, fmt.Sprintf("saved-%06d", i))
		if err != nil {
			return err
		}
		repair.Restores = append(repair.Restores, *restore)
	} else if available && file.Backup != nil {
		slot := fmt.Sprintf("restore-%06d", i)
		image, err := inspectRecoveryImage(private, slot)
		if err != nil {
			return err
		}
		if image == nil {
			copy, err := private.copyImage(ctx, preimageName(i), private.root, file.Backup)
			if err != nil {
				return err
			}
			if err = renameMigrationExclusive(copy, filepath.Join(private.root, slot)); err != nil {
				return err
			}
			image, err = inspectRecoveryImage(private, slot)
			if err != nil {
				return err
			}
		}
		if !sameImageContent(image, file.Backup) {
			return fmt.Errorf("restoration copy changed: %s", file.Path)
		}
		repair.Restores = append(repair.Restores, recoveryRestore{Path: file.Path, Slot: slot, VersionSlot: preimageName(i), Image: image})
	}
	for _, version := range []*recoveryVersion{retained, staged} {
		if version != nil && !(available && chosen == version) {
			repair.Versions = append(repair.Versions, *version)
		}
	}
	return nil
}

var errRecoveryDestinationChanged = errors.New("canonical recovery destination changed")

// During planning, a missing original parent may itself be scheduled for
// identity-preserving restoration. Replay rechecks the actual public binding.
func (r *Repository) canPlanRestore(plan *worktreeIntent, repair *recoveryPlan, name string) (string, bool, error) {
	p, available, err := r.canRestoreOriginal(plan, name)
	if available || err != nil {
		return p, available, err
	}
	for parent := filepath.ToSlash(filepath.Dir(name)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
		expected := ""
		for _, dir := range plan.Directories {
			if dir.Path == parent && !dir.Staged {
				expected = dir.Identity
				break
			}
		}
		if expected == "" {
			return "", false, nil
		}
		path := filepath.Join(r.root, filepath.FromSlash(parent))
		info, err := os.Lstat(path)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", false, nil
			}
			id, err := filesystemIdentity(path)
			if err != nil {
				return "", false, err
			}
			if id != expected {
				return "", false, nil
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		planned := false
		for _, restore := range repair.Restores {
			if restore.Path == parent && restore.Directory == expected {
				planned = true
				break
			}
		}
		if !planned {
			return "", false, nil
		}
	}
	p = filepath.Join(r.root, filepath.FromSlash(name))
	if _, err := os.Lstat(p); err == nil {
		return "", false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	return p, true, nil
}

func (r *Repository) prepareRecoveryDirectories(ctx context.Context, plan *worktreeIntent, repair *recoveryPlan) error {
	private := &Repository{root: r.journalPath()}
	for i, dir := range plan.Directories {
		staged, err := captureRecoveryVersion(private, directoryName(i), dir.Path, "staged-directory")
		if err != nil {
			return err
		}
		if staged != nil {
			repair.Versions = append(repair.Versions, *staged)
		}
		if !dir.Remove {
			continue
		}
		retained, err := captureRecoveryVersion(private, retainedDirectoryName(i), dir.Path, "retained-directory")
		if err != nil {
			return err
		}
		if retained == nil {
			continue
		}
		newer := false
		for j, file := range plan.Files {
			if file.Path != dir.Path {
				continue
			}
			target, err := captureRecoveryVersion(private, stageName(j), file.Path, "")
			if err != nil {
				return err
			}
			newer = target != nil && !equalImage(target.Image, file.Target)
			break
		}
		_, available, err := r.canPlanRestore(plan, repair, dir.Path)
		if err != nil {
			return err
		}
		if available && !newer {
			restore, err := r.saveRecoverySource(ctx, repair, retained, fmt.Sprintf("saved-dir-%06d", i))
			if err != nil {
				return err
			}
			repair.Restores = append(repair.Restores, *restore)
		} else {
			repair.Versions = append(repair.Versions, *retained)
		}
	}
	return nil
}

func validateRecoveryRestores(plan *worktreeIntent, repair *recoveryPlan) error {
	type ownership struct{ path, version string }
	allowed := map[string]ownership{}
	for i, file := range plan.Files {
		allowed[retainedName(i)] = ownership{file.Path, fmt.Sprintf("saved-%06d", i)}
		allowed[stageName(i)] = ownership{file.Path, fmt.Sprintf("saved-%06d", i)}
		if file.Backup != nil {
			allowed[fmt.Sprintf("restore-%06d", i)] = ownership{file.Path, preimageName(i)}
		}
	}
	for i, dir := range plan.Directories {
		if dir.Remove {
			allowed[retainedDirectoryName(i)] = ownership{dir.Path, fmt.Sprintf("saved-dir-%06d", i)}
		}
	}
	versions := map[string]recoveryVersion{}
	for _, version := range repair.Versions {
		if _, exists := versions[version.Slot]; exists || version.Path == "" || version.Image == nil && version.Directory == "" || version.Image != nil && version.Directory != "" {
			return fmt.Errorf("invalid preserved-version ownership set")
		}
		versions[version.Slot] = version
	}
	seen := map[string]bool{}
	for _, restore := range repair.Restores {
		owner, ok := allowed[restore.Slot]
		saved, backed := versions[restore.VersionSlot]
		if !ok || owner.path != restore.Path || owner.version != restore.VersionSlot || !backed || saved.Path != restore.Path || seen[restore.Path] {
			return fmt.Errorf("invalid automatic restoration source")
		}
		if restore.Image != nil {
			if restore.Directory != "" || restore.Image.Identity == "" || !sameImageContent(restore.Image, saved.Image) || restore.Image.Identity == saved.Image.Identity {
				return fmt.Errorf("automatic restoration lacks independent saved bytes")
			}
		} else if restore.Directory == "" || saved.Directory == "" || restore.Directory == saved.Directory {
			return fmt.Errorf("automatic directory restoration lacks an independent saved version")
		}
		seen[restore.Path] = true
	}
	return nil
}

func (r *Repository) restorationStatus(archive string, restore recoveryRestore) RecoveryRestoration {
	result := RecoveryRestoration{Path: restore.Path, Location: filepath.Join(archive, restore.VersionSlot)}
	canonical, err := captureRecoveryVersion(r, restore.Path, restore.Path, "")
	if err == nil && canonical != nil && (restore.Image != nil && equalImage(canonical.Image, restore.Image) || restore.Directory != "" && canonical.Directory == restore.Directory) {
		result.Location = filepath.Join(r.root, filepath.FromSlash(restore.Path))
		result.Canonical = true
		return result
	}
	retained, err := captureRecoveryVersion(&Repository{root: archive}, restore.Slot, restore.Path, "")
	if err == nil && retained != nil && (restore.Image != nil && equalImage(retained.Image, restore.Image) || restore.Directory != "" && retained.Directory == restore.Directory) {
		result.Location = filepath.Join(archive, restore.Slot)
		return result
	}
	// Never overwrite a later canonical edit to replay an already consumed
	// restoration. Its independently saved version remains at VersionSlot.
	result.Changed = true
	return result
}
