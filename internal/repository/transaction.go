package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

// Journal versioning is local repository coordination, not an object codec or a
// format-2 change. Intent is immutable; names/identities encode rename progress.
const maxJournalBytes = 64 << 20

type fileImage struct {
	Identity      string
	Size          uint64
	Sum           [32]byte
	Executable    bool
	Symlink       bool
	DirectoryLink bool
	Modified      int64
}
type journalFile struct {
	Path     string
	Original *fileImage
	Target   *fileImage
	Backup   *fileImage `json:",omitempty"`
	Keep     bool
}
type journalDirectory struct {
	Path     string
	Identity string
	Staged   bool
	Remove   bool
	Absent   bool `json:",omitempty"`
}
type worktreeIntent struct {
	RecoveryID    string `json:",omitempty"`
	version       int
	RootIdentity  string
	Kind          string
	RestorePath   string
	BeforeHead    string
	AfterHead     string
	BeforeID      storev2.ID
	AfterID       storev2.ID
	Files         []journalFile
	Directories   []journalDirectory
	RestoreSource *storev2.ID `json:",omitempty"`
	RestoreIndex  *storev2.ID `json:",omitempty"`
}
type journalEnvelope struct {
	Version int
	Intent  json.RawMessage
	SHA256  string
}

func (r *Repository) journalPath() string {
	return filepath.Join(r.root, ".pebble", "worktree-journal")
}
func stageName(i int) string     { return fmt.Sprintf("new-%06d", i) }
func retainedName(i int) string  { return fmt.Sprintf("old-%06d", i) }
func directoryName(i int) string { return fmt.Sprintf("dir-%06d", i) }
func (r *Repository) boundary(ctx context.Context, stage, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.fault != nil {
		return r.fault(stage, path)
	}
	return nil
}

func inspectImage(r *Repository, name string) (*fileImage, error) {
	p, err := r.safePath(name)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return nil, fmt.Errorf("not a regular file or symbolic link: %s", name)
	}
	identity, err := filesystemIdentity(p)
	if err != nil {
		return nil, err
	}
	hash, err := r.hashFile(name)
	if err != nil {
		return nil, err
	}
	after, err := filesystemIdentity(p)
	if err != nil {
		return nil, err
	}
	final, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if identity != after || !os.SameFile(info, final) || info.ModTime() != final.ModTime() || info.Mode() != final.Mode() || info.Size() != final.Size() {
		return nil, fmt.Errorf("file changed while recording identity: %s", name)
	}
	return &fileImage{Identity: identity, Size: hash.size, Sum: hash.sum, Executable: hash.executable, Symlink: hash.symlink, DirectoryLink: hash.directoryLink, Modified: info.ModTime().UnixNano()}, nil
}
func equalImage(a, b *fileImage) bool { return a != nil && b != nil && *a == *b }
func strictJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing journal data: %v", err)
	}
	return nil
}
func writeIntent(dir string, plan *worktreeIntent) error {
	envelope, err := encodeWorktreeRecord(plan, plan.version)
	if err != nil {
		return err
	}
	// publishMigration subsequently syncs every stage file and directory.
	return os.WriteFile(filepath.Join(dir, "intent.json"), envelope, 0600)
}
func (r *Repository) readIntent() (*worktreeIntent, error) {
	dir := r.journalPath()
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("journal is not an ordinary directory")
	}
	var data []byte
	err = (&Repository{root: dir}).readFile("intent.json", func(f *os.File) error {
		var err error
		data, err = io.ReadAll(io.LimitReader(f, maxJournalBytes+1))
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(data) > maxJournalBytes {
		return nil, fmt.Errorf("oversized journal")
	}
	var envelope journalEnvelope
	if err = strictJSON(data, &envelope); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(envelope.Intent)
	if envelope.Version < 1 || envelope.Version > 4 || envelope.SHA256 != hex.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("unsupported or corrupt worktree journal")
	}
	var plan worktreeIntent
	plan.version = envelope.Version
	if err = strictJSON(envelope.Intent, &plan); err != nil {
		return nil, err
	}
	if err = r.validateIntent(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func imageMatchesEntry(s *storev2.Store, image *fileImage, entry storev2.TreeEntry) error {
	if image == nil || image.Identity == "" {
		return fmt.Errorf("missing file identity: %s", entry.Path)
	}
	length, sum, err := s.FileInfo(context.Background(), entry.Manifest)
	if err != nil {
		return err
	}
	if image.Size != length || image.Sum != sum || measuresExecutable && image.Executable != entry.Executable || image.Symlink != entry.Symlink || measuresDirectoryLink && image.DirectoryLink != entry.DirectoryLink {
		return fmt.Errorf("journal file does not match revision: %s", entry.Path)
	}
	return nil
}
func (r *Repository) validateIntent(plan *worktreeIntent) error {
	root, err := filesystemIdentity(r.root)
	if err != nil {
		return err
	}
	if root != plan.RootIdentity {
		return fmt.Errorf("journal belongs to another working tree")
	}
	if plan.version >= 3 {
		if !strings.HasPrefix(plan.RecoveryID, "worktree-stage-") || strings.ContainsAny(plan.RecoveryID, `/\`) {
			return fmt.Errorf("invalid recovery transaction identity")
		}
	} else if plan.RecoveryID != "" {
		return fmt.Errorf("legacy journal contains new recovery semantics")
	}
	if plan.version < 4 && (plan.RestoreSource != nil || plan.RestoreIndex != nil) ||
		plan.version == 4 && (plan.Kind != "restore" || plan.RestoreSource == nil) {
		return fmt.Errorf("unsupported restore source semantics")
	}
	if plan.Kind != "checkout" && plan.Kind != "restore" {
		return fmt.Errorf("unsupported journal operation")
	}
	for _, ref := range []string{plan.BeforeHead, plan.AfterHead} {
		if !strings.HasPrefix(ref, "heads/") || !storev2.ValidBranch(strings.TrimPrefix(ref, "heads/")) {
			return fmt.Errorf("invalid journal HEAD")
		}
	}
	before, err := r.tree(plan.BeforeID)
	if err != nil {
		return err
	}
	after, err := r.tree(plan.AfterID)
	if err != nil {
		return err
	}
	if plan.Kind == "restore" {
		if plan.BeforeHead != plan.AfterHead || plan.BeforeID != plan.AfterID {
			return fmt.Errorf("restore journal changes history")
		}
		if plan.version == 4 {
			before, after, err = r.restoreSelection(plan)
			if err != nil {
				return err
			}
		} else {
			selected := []storev2.TreeEntry{}
			for _, e := range after {
				if e.Path == plan.RestorePath {
					selected = append(selected, e)
				}
			}
			if len(selected) != 1 {
				return fmt.Errorf("invalid restore journal path")
			}
			before, after = nil, selected
		}
	} else if plan.RestorePath != "" {
		return fmt.Errorf("unexpected restore path")
	}
	old, target := map[string]storev2.TreeEntry{}, map[string]storev2.TreeEntry{}
	names := map[string]bool{}
	for _, e := range before {
		old[e.Path] = e
		names[e.Path] = true
	}
	for _, e := range after {
		target[e.Path] = e
		names[e.Path] = true
	}
	if len(plan.Files) != len(names) || len(plan.Files) > storev2.MaxGraphObjects {
		return fmt.Errorf("incomplete journal file set")
	}
	parents := map[string]bool{}
	previous := ""
	for _, file := range plan.Files {
		if !names[file.Path] || file.Path <= previous {
			return fmt.Errorf("invalid journal file ordering")
		}
		previous = file.Path
		if err = validPath(file.Path); err != nil {
			return err
		}
		for parent := filepath.ToSlash(filepath.Dir(file.Path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			parents[parent] = true
		}
		for _, image := range []*fileImage{file.Original, file.Target, file.Backup} {
			if image != nil && image.Identity == "" {
				return fmt.Errorf("empty journal identity")
			}
		}
		if plan.version >= 3 && !file.Keep && file.Original != nil && !sameImageContent(file.Backup, file.Original) || (file.Original == nil || file.Keep) && file.Backup != nil {
			return fmt.Errorf("missing or inconsistent journal preimage: %s", file.Path)
		}
		if entry, ok := old[file.Path]; ok && plan.Kind == "checkout" {
			if err = imageMatchesEntry(r.store, file.Original, entry); err != nil {
				return err
			}
		} else if (plan.Kind == "checkout" || plan.version == 4) && !ok && file.Original != nil && !file.Keep {
			return fmt.Errorf("journal overwrites untracked path")
		}
		if entry, ok := target[file.Path]; ok {
			if err = imageMatchesEntry(r.store, file.Target, entry); err != nil {
				return err
			}
		} else if file.Target != nil {
			return fmt.Errorf("unexpected target image")
		}
		if file.Keep && !equalImage(file.Original, file.Target) {
			return fmt.Errorf("invalid adopted file identity")
		}
	}
	seen := map[string]bool{}
	for _, dir := range plan.Directories {
		removable := false
		for name := range target {
			if dir.Path == name || strings.HasPrefix(dir.Path, name+"/") {
				removable = true
				break
			}
		}
		if err = validPath(dir.Path); err != nil {
			return err
		}
		if dir.Absent && (plan.version != 4 || dir.Identity != "" || dir.Staged || dir.Remove) {
			return fmt.Errorf("invalid absent restore directory")
		}
		if seen[dir.Path] || dir.Identity == "" && !dir.Absent || dir.Staged && dir.Remove || dir.Remove && !removable || !parents[dir.Path] && !dir.Remove {
			return fmt.Errorf("invalid journal directory")
		}
		parent := filepath.ToSlash(filepath.Dir(dir.Path))
		if parent != "." && !seen[parent] {
			return fmt.Errorf("unordered journal directories")
		}
		seen[dir.Path] = true
		delete(parents, dir.Path)
	}
	if len(parents) != 0 {
		return fmt.Errorf("incomplete journal directories")
	}
	return nil
}

func (r *Repository) inspectJournalFile(stage string, i int, file journalFile, plan *worktreeIntent) error {
	backup, err := inspectImage(&Repository{root: stage}, preimageName(i))
	if err != nil {
		return err
	}
	if file.Backup != nil && !equalImage(backup, file.Backup) || file.Backup == nil && backup != nil {
		return fmt.Errorf("preimage recovery material changed: %s", file.Path)
	}
	live, err := r.inspectLiveImage(plan, file.Path)
	if err != nil {
		return err
	}
	private := &Repository{root: stage}
	staged, err := inspectImage(private, stageName(i))
	if err != nil {
		return err
	}
	retained, err := inspectImage(private, retainedName(i))
	if err != nil {
		return err
	}
	conflict := func() error {
		return fmt.Errorf("external change or incomplete recovery material for %s; preserve journal and working files", file.Path)
	}
	if file.Keep {
		if !equalImage(live, file.Target) || staged != nil || retained != nil {
			return conflict()
		}
		return nil
	}
	if file.Original != nil {
		if equalImage(live, file.Original) {
			if retained != nil || file.Target != nil && !equalImage(staged, file.Target) {
				return conflict()
			}
		} else if !equalImage(retained, file.Original) {
			return conflict()
		}
	} else if retained != nil {
		return conflict()
	}
	if file.Target != nil {
		if equalImage(staged, file.Target) {
			if live != nil && !equalImage(live, file.Original) {
				return conflict()
			}
		} else if staged != nil || !equalImage(live, file.Target) {
			return conflict()
		}
	} else if staged != nil || live != nil && !equalImage(live, file.Original) {
		return conflict()
	}
	return nil
}
func (r *Repository) inspectHistory(plan *worktreeIntent) error {
	if plan.RestoreSource != nil {
		if err := r.store.Verify(context.Background(), *plan.RestoreSource); err != nil {
			return err
		}
	}
	head, err := r.GetHead()
	if err != nil {
		return err
	}
	if head != plan.BeforeHead && head != plan.AfterHead {
		return fmt.Errorf("HEAD changed outside recovery")
	}
	for i, pin := range []struct {
		name string
		id   storev2.ID
	}{{plan.BeforeHead, plan.BeforeID}, {plan.AfterHead, plan.AfterID}} {
		if i == 1 && plan.BeforeHead == plan.AfterHead && plan.BeforeID == plan.AfterID {
			continue
		}
		id, err := r.GetRef(pin.name)
		if pin.id == (storev2.ID{}) && os.IsNotExist(err) && pin.name == "heads/main" {
			continue
		}
		if err != nil {
			return err
		}
		if id != pin.id {
			return fmt.Errorf("branch changed outside recovery: %s", pin.name)
		}
	}
	return nil
}

func (r *Repository) updateIntentHead(ctx context.Context, from, to string, plan *worktreeIntent) error {
	before := storev2.HeadPin{Name: plan.BeforeHead, ID: plan.BeforeID}
	if plan.BeforeHead == plan.AfterHead && plan.BeforeID == plan.AfterID {
		return r.store.UpdateHead(ctx, from, to, before)
	}
	return r.store.UpdateHead(ctx, from, to, before,
		storev2.HeadPin{Name: plan.AfterHead, ID: plan.AfterID})
}
func (r *Repository) inspectTransaction(stage string, plan *worktreeIntent) error {
	if err := r.inspectHistory(plan); err != nil {
		return err
	}
	if err := r.inspectDirectories(stage, plan); err != nil {
		return err
	}
	for i, file := range plan.Files {
		if err := r.inspectJournalFile(stage, i, file, plan); err != nil {
			return err
		}
	}
	return nil
}

// Recover explicitly resumes an operation or safely aborts it when external
// changes prevent replay. An abort preserves versions at discoverable paths;
// it never grants authority to replace a subsequent external edit.
// Reopening remains read-only; callers choose when to invoke recovery.
func (r *Repository) Recover(ctx context.Context) (err error) {
	unlock, err := r.operationLock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err = r.boundary(ctx, "recover-open", r.journalPath()); err != nil {
		return err
	}
	plan, err := r.readIntent()
	if os.IsNotExist(err) {
		// A missing intent inside an existing journal is corruption, not completion.
		if _, exists := os.Lstat(r.journalPath()); os.IsNotExist(exists) {
			return r.finishPublishedRecoveries()
		}
	}
	if err != nil {
		return fmt.Errorf("recovery refused; preserve journal: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	var repair recoveryPlan
	if err = readRecoveryRecord(r.journalPath(), "repair.json", &repair); err == nil {
		return r.resumeRecovery(ctx, plan, &repair)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reconciliation record refused: %w", err)
	}
	if err = r.inspectTransaction(r.journalPath(), plan); err != nil {
		prepared, prepareErr := r.prepareRecovery(ctx, plan, err)
		if prepareErr != nil {
			return errors.Join(err, prepareErr)
		}
		return r.resumeRecovery(ctx, plan, prepared)
	}
	return r.resumeWorktree(ctx, plan)
}
func (r *Repository) transactionMove(ctx context.Context, stage, source, target string, image *fileImage, publicSource bool, ownership ...func() error) error {
	identity, err := filesystemIdentity(source)
	if err != nil {
		return err
	}
	if image != nil && identity != image.Identity {
		return fmt.Errorf("rename source ownership changed: %s", source)
	}
	parents := map[string]string{}
	for _, path := range []string{filepath.Dir(source), filepath.Dir(target)} {
		for {
			id, err := filesystemIdentity(path)
			if err != nil {
				return err
			}
			parents[path] = id
			next := filepath.Dir(path)
			if next == path {
				break
			}
			path = next
		}
	}
	if err = r.boundary(ctx, stage, target); err != nil {
		return err
	}
	parentsUnchanged := func() error {
		for path, want := range parents {
			got, err := filesystemIdentity(path)
			if err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("namespace ownership changed: %s", path)
			}
		}
		return nil
	}
	check := func() error {
		if err := r.boundary(ctx, stage+"-anchored", target); err != nil {
			return err
		}
		if err := parentsUnchanged(); err != nil {
			return err
		}
		for _, verify := range ownership {
			if err := verify(); err != nil {
				return err
			}
		}
		return nil
	}
	if err = anchoredMove(source, target, identity, image, publicSource, check,
		func() error { return r.boundary(ctx, stage+"-rename", target) },
		func() error { return r.boundary(ctx, stage+"-renamed", target) },
		parentsUnchanged, func() error { return r.boundary(ctx, stage+"-barrier", target) }); err != nil {
		return fmt.Errorf("%s namespace move: %w", stage, err)
	}
	return r.boundary(ctx, stage+"-published", target)
}
func (r *Repository) resumeWorktree(ctx context.Context, plan *worktreeIntent) error {
	stage := r.journalPath()
	if err := r.inspectTransaction(stage, plan); err != nil {
		return err
	}
	check := func() error { return r.inspectTransaction(stage, plan) }
	// Retain original leaves before any directory transition. No unlink/copy.
	for i, file := range plan.Files {
		if file.Keep || file.Original == nil {
			continue
		}
		original := filepath.Join(stage, retainedName(i))
		if _, err := os.Lstat(original); os.IsNotExist(err) {
			if err = check(); err != nil {
				return err
			}
			target, err := r.safePath(file.Path)
			if err != nil {
				return err
			}
			if err = r.transactionMove(ctx, "retain", target, original, file.Original, true, check); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	// Move only verified empty directories, deepest first. Retained directories
	// stay owned by the same immutable intent through interruption and retries.
	for i := len(plan.Directories) - 1; i >= 0; i-- {
		dir := plan.Directories[i]
		if !dir.Remove {
			continue
		}
		slot := filepath.Join(stage, retainedDirectoryName(i))
		if _, err := os.Lstat(slot); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		p, err := r.safePath(dir.Path)
		if err != nil {
			return err
		}
		children, err := readDirectory(p)
		if err != nil {
			return err
		}
		if len(children) != 0 {
			return fmt.Errorf("directory gained untracked children: %s", dir.Path)
		}
		if err = r.transactionMove(ctx, "retain-directory", p, slot, nil, true, check); err != nil {
			return err
		}
	}
	for i, dir := range plan.Directories {
		if !dir.Staged {
			continue
		}
		source := filepath.Join(stage, directoryName(i))
		if _, err := os.Lstat(source); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		target, err := r.safePath(dir.Path)
		if err != nil {
			return err
		}
		if err = r.recordMove(ctx, plan, "directory", i, dir.Identity); err != nil {
			return err
		}
		if err = r.transactionMove(ctx, "directory", source, target, nil, false, check); err != nil {
			return err
		}
	}
	for i, file := range plan.Files {
		if file.Keep || file.Target == nil {
			continue
		}
		source := filepath.Join(stage, stageName(i))
		if _, err := os.Lstat(source); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		target, err := r.safePath(file.Path)
		if err != nil {
			return err
		}
		if err = r.recordMove(ctx, plan, "install", i, file.Target.Identity); err != nil {
			return err
		}
		if err = r.transactionMove(ctx, "install", source, target, file.Target, false, check); err != nil {
			return err
		}
	}
	if err := r.inspectTransaction(stage, plan); err != nil {
		return err
	}
	if err := r.boundary(ctx, "namespace-sync", stage); err != nil {
		return err
	}
	if err := settleWorktree(ctx, r, stage, plan); err != nil {
		return err
	}
	if err := r.inspectTransaction(stage, plan); err != nil {
		return err
	}
	if err := r.boundary(ctx, "head", plan.AfterHead); err != nil {
		return err
	}
	if err := r.inspectTransaction(stage, plan); err != nil {
		return err
	}
	head, err := r.GetHead()
	if err != nil {
		return err
	}
	if head != plan.BeforeHead && head != plan.AfterHead {
		return fmt.Errorf("HEAD changed outside recovery")
	}
	// Identical retry republishes through the store's CAS and durability barrier.
	if err = r.updateIntentHead(ctx, head, plan.AfterHead, plan); err != nil {
		return err
	}
	if err = r.boundary(ctx, "head-published", plan.AfterHead); err != nil {
		return err
	}
	if err = r.inspectTransaction(stage, plan); err != nil {
		return err
	}
	completed, err := os.MkdirTemp(filepath.Dir(stage), "worktree-completed-")
	if err != nil {
		return err
	}
	if err = os.Remove(completed); err != nil {
		return err
	}
	// Remove only after a durable rename retires the entire active journal.
	// A cleanup interruption can leave private debris, never a partial active intent.
	if err = r.transactionMove(ctx, "complete", stage, completed, nil, false, check); err != nil {
		return err
	}
	return os.RemoveAll(completed)
}
