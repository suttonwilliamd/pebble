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
	"sort"
	"strings"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

// Journal versioning is local repository coordination, not an object codec or a
// format-2 change. Intent is immutable; names/identities encode rename progress.
const maxJournalBytes = 64 << 20

type fileImage struct {
	Identity   string
	Size       uint64
	Sum        [32]byte
	Executable bool
	Modified   int64
}
type journalFile struct {
	Path     string
	Original *fileImage
	Target   *fileImage
	Keep     bool
}
type journalDirectory struct {
	Path     string
	Identity string
	Staged   bool
}
type worktreeIntent struct {
	RootIdentity string
	Kind         string
	RestorePath  string
	BeforeHead   string
	AfterHead    string
	BeforeID     storev2.ID
	AfterID      storev2.ID
	Files        []journalFile
	Directories  []journalDirectory
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
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular working file: %s", name)
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
	return &fileImage{identity, hash.size, hash.sum, hash.executable, info.ModTime().UnixNano()}, nil
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
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	envelope, err := json.Marshal(journalEnvelope{1, data, hex.EncodeToString(sum[:])})
	if err != nil {
		return err
	}
	if len(envelope) > maxJournalBytes {
		return fmt.Errorf("worktree journal exceeds %d bytes", maxJournalBytes)
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
	if envelope.Version != 1 || envelope.SHA256 != hex.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("unsupported or corrupt worktree journal")
	}
	var plan worktreeIntent
	if err = strictJSON(envelope.Intent, &plan); err != nil {
		return nil, err
	}
	if err = r.validateIntent(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (r *Repository) prepareDirectories(stage string, plan *worktreeIntent) error {
	var err error
	plan.RootIdentity, err = filesystemIdentity(r.root)
	if err != nil {
		return err
	}
	parents := map[string]bool{}
	for _, file := range plan.Files {
		for parent := filepath.ToSlash(filepath.Dir(file.Path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			parents[parent] = true
		}
	}
	names := make([]string, 0, len(parents))
	for name := range parents {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := strings.Count(names[i], "/"), strings.Count(names[j], "/")
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
	for i, name := range names {
		p, err := r.safePath(name)
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		staged := os.IsNotExist(err)
		if err != nil && !staged {
			return err
		}
		if staged {
			p = filepath.Join(stage, directoryName(i))
			if err = os.Mkdir(p, 0755); err != nil {
				return err
			}
		} else if !info.IsDir() {
			return fmt.Errorf("file blocks directory: %s", name)
		}
		identity, err := filesystemIdentity(p)
		if err != nil {
			return err
		}
		plan.Directories = append(plan.Directories, journalDirectory{name, identity, staged})
	}
	return nil
}
func imageMatchesEntry(s *storev2.Store, image *fileImage, entry storev2.TreeEntry) error {
	if image == nil || image.Identity == "" {
		return fmt.Errorf("missing file identity: %s", entry.Path)
	}
	length, sum, err := s.FileInfo(context.Background(), entry.Manifest)
	if err != nil {
		return err
	}
	if image.Size != length || image.Sum != sum || image.Executable != entry.Executable {
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
	entries := make([]storev2.TreeEntry, 0, len(plan.Files))
	parents := map[string]bool{}
	previous := ""
	for _, file := range plan.Files {
		if !names[file.Path] || file.Path <= previous {
			return fmt.Errorf("invalid journal file ordering")
		}
		previous = file.Path
		entries = append(entries, storev2.TreeEntry{Path: file.Path})
		for parent := filepath.ToSlash(filepath.Dir(file.Path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			parents[parent] = true
		}
		for _, image := range []*fileImage{file.Original, file.Target} {
			if image != nil && image.Identity == "" {
				return fmt.Errorf("empty journal identity")
			}
		}
		if entry, ok := old[file.Path]; ok {
			if err = imageMatchesEntry(r.store, file.Original, entry); err != nil {
				return err
			}
		} else if plan.Kind == "checkout" && file.Original != nil && !file.Keep {
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
	if err = validateEntries(entries); err != nil {
		return err
	}
	if len(parents) != len(plan.Directories) {
		return fmt.Errorf("incomplete journal directories")
	}
	seen := map[string]bool{}
	for _, dir := range plan.Directories {
		if !parents[dir.Path] || seen[dir.Path] || dir.Identity == "" {
			return fmt.Errorf("invalid journal directory")
		}
		parent := filepath.ToSlash(filepath.Dir(dir.Path))
		if parent != "." && !seen[parent] {
			return fmt.Errorf("unordered journal directories")
		}
		seen[dir.Path] = true
	}
	return nil
}

func (r *Repository) inspectDirectories(stage string, plan *worktreeIntent) error {
	for i, dir := range plan.Directories {
		p, err := r.safePath(dir.Path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if os.IsNotExist(err) && dir.Staged {
			p = filepath.Join(stage, directoryName(i))
			info, err = os.Lstat(p)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("directory replaced: %s", dir.Path)
		}
		identity, err := filesystemIdentity(p)
		if err != nil {
			return err
		}
		if identity != dir.Identity {
			return fmt.Errorf("external directory change: %s", dir.Path)
		}
		if p != filepath.Join(stage, directoryName(i)) && dir.Staged {
			if _, err = os.Lstat(filepath.Join(stage, directoryName(i))); !os.IsNotExist(err) {
				return fmt.Errorf("ambiguous staged directory: %s", dir.Path)
			}
		}
	}
	return nil
}
func (r *Repository) inspectJournalFile(stage string, i int, file journalFile) error {
	live, err := inspectImage(r, file.Path)
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
func (r *Repository) inspectTransaction(stage string, plan *worktreeIntent) error {
	head, err := r.GetHead()
	if err != nil {
		return err
	}
	if head != plan.BeforeHead && head != plan.AfterHead {
		return fmt.Errorf("HEAD changed outside recovery")
	}
	for _, pin := range []struct {
		name string
		id   storev2.ID
	}{{plan.BeforeHead, plan.BeforeID}, {plan.AfterHead, plan.AfterID}} {
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
	if err = r.inspectDirectories(stage, plan); err != nil {
		return err
	}
	for i, file := range plan.Files {
		if err = r.inspectJournalFile(stage, i, file); err != nil {
			return err
		}
	}
	return nil
}

// Recover explicitly resumes a pending operation. It never guesses ownership
// from contents alone or clears legacy locks; unexpected changes fail closed.
// Reopening is read-only; callers choose when to resume via this API/CLI.
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
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("recovery refused; preserve journal: %w", err)
	}
	return r.resumeWorktree(ctx, plan)
}
func (r *Repository) transactionMove(ctx context.Context, stage, source, target string) error {
	if err := r.boundary(ctx, stage, target); err != nil {
		return err
	}
	if err := moveWorktree(source, target, func() error { return r.boundary(ctx, stage+"-barrier", target) }); err != nil {
		return err
	}
	return r.boundary(ctx, stage+"-published", target)
}
func (r *Repository) resumeWorktree(ctx context.Context, plan *worktreeIntent) error {
	stage := r.journalPath()
	if err := r.inspectTransaction(stage, plan); err != nil {
		return err
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
		target, err := r.safePath(dir.Path)
		if err != nil {
			return err
		}
		if err = r.transactionMove(ctx, "directory", source, target); err != nil {
			return err
		}
	}
	for i, file := range plan.Files {
		if err := r.inspectDirectories(stage, plan); err != nil {
			return err
		}
		if err := r.inspectJournalFile(stage, i, file); err != nil {
			return err
		}
		if file.Keep {
			continue
		}
		target, err := r.safePath(file.Path)
		if err != nil {
			return err
		}
		original := filepath.Join(stage, retainedName(i))
		if file.Original != nil {
			if _, err = os.Lstat(original); os.IsNotExist(err) {
				if err = r.transactionMove(ctx, "retain", target, original); err != nil {
					return err
				}
				if err = r.inspectJournalFile(stage, i, file); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
		}
		if file.Target != nil {
			source := filepath.Join(stage, stageName(i))
			if _, err = os.Lstat(source); err == nil {
				if err = r.transactionMove(ctx, "install", source, target); err != nil {
					return err
				}
			} else if !os.IsNotExist(err) {
				return err
			}
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
	head, err := r.GetHead()
	if err != nil {
		return err
	}
	if head != plan.BeforeHead && head != plan.AfterHead {
		return fmt.Errorf("HEAD changed outside recovery")
	}
	// Identical retry republishes through the store's CAS and durability barrier.
	if err = r.store.UpdateHead(ctx, head, plan.AfterHead); err != nil {
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
	if err = r.transactionMove(ctx, "complete", stage, completed); err != nil {
		return err
	}
	return os.RemoveAll(completed)
}
