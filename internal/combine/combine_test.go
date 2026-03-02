package combine

import (
	"bytes"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/snapshot"
)

func TestConflictType_String(t *testing.T) {
	tests := []struct {
		ct    ConflictType
		want  string
	}{
		{ConflictTypeBothModified, "both_modified"},
		{ConflictTypeDeleted, "deleted"},
		{ConflictTypeAdded, "added"},
		{ConflictTypeRenamed, "renamed"},
	}

	for _, tt := range tests {
		if got := string(tt.ct); got != tt.want {
			t.Errorf("ConflictType.String() = %v, want %v", got, tt.want)
		}
	}
}

func TestFileConflict_IsConflict(t *testing.T) {
	conflict := FileConflict{
		Path:         "test.txt",
		ConflictType: ConflictTypeBothModified,
		BaseHash:     "abc123",
		OurHash:      "def456",
		TheirHash:    "ghi789",
	}

	if conflict.Path != "test.txt" {
		t.Errorf("Path = %v, want test.txt", conflict.Path)
	}
	if conflict.ConflictType != ConflictTypeBothModified {
		t.Errorf("ConflictType = %v, want both_modified", conflict.ConflictType)
	}
}

func TestResolution_String(t *testing.T) {
	res := Resolution{
		Path:    "test.txt",
		Content: []byte("content"),
		Action:  "ours",
	}

	if res.Action != "ours" {
		t.Errorf("Action = %v, want ours", res.Action)
	}
	if string(res.Content) != "content" {
		t.Errorf("Content = %v, want content", string(res.Content))
	}
}

func TestNewComparator(t *testing.T) {
	comp := NewComparator("/test/path")
	if comp.baseDir != "/test/path" {
		t.Errorf("baseDir = %v, want /test/path", comp.baseDir)
	}
}

func TestResolveConflict_Ours(t *testing.T) {
	conflict := FileConflict{
		Path:         "test.txt",
		ConflictType: ConflictTypeBothModified,
		OurContent:   []byte("our content"),
		TheirContent: []byte("their content"),
		BaseContent:  []byte("base content"),
	}

	res := ResolveConflict(conflict, "ours")
	if res.Action != "ours" {
		t.Errorf("Action = %v, want ours", res.Action)
	}
	if !bytes.Equal(res.Content, conflict.OurContent) {
		t.Errorf("Content = %v, want our content", string(res.Content))
	}
}

func TestResolveConflict_Theirs(t *testing.T) {
	conflict := FileConflict{
		Path:         "test.txt",
		ConflictType: ConflictTypeBothModified,
		OurContent:   []byte("our content"),
		TheirContent: []byte("their content"),
	}

	res := ResolveConflict(conflict, "theirs")
	if res.Action != "theirs" {
		t.Errorf("Action = %v, want theirs", res.Action)
	}
	if !bytes.Equal(res.Content, conflict.TheirContent) {
		t.Errorf("Content = %v, want their content", string(res.Content))
	}
}

func TestResolveConflict_Delete(t *testing.T) {
	conflict := FileConflict{
		Path:         "test.txt",
		ConflictType: ConflictTypeDeleted,
		OurContent:   []byte("our content"),
	}

	res := ResolveConflict(conflict, "delete")
	if res.Action != "delete" {
		t.Errorf("Action = %v, want delete", res.Action)
	}
	if res.Content != nil {
		t.Errorf("Content = %v, want nil", res.Content)
	}
}

func TestResolveConflict_Default(t *testing.T) {
	conflict := FileConflict{
		Path:         "test.txt",
		ConflictType: ConflictTypeBothModified,
		OurContent:   []byte("our content"),
		TheirContent: []byte("their content"),
		BaseContent:  []byte("base content"),
	}

	res := ResolveConflict(conflict, "unknown")
	if res.Action != "unknown" {
		t.Errorf("Action = %v, want unknown", res.Action)
	}
	// Default should use base content
	if !bytes.Equal(res.Content, conflict.BaseContent) {
		t.Errorf("Content = %v, want base content", string(res.Content))
	}
}

func TestNewTreeWalker(t *testing.T) {
	tw := NewTreeWalker("/test/root")
	if tw.rootPath != "/test/root" {
		t.Errorf("rootPath = %v, want /test/root", tw.rootPath)
	}
}

func TestTreeWalker_Walk(t *testing.T) {
	tw := NewTreeWalker("/test")

	tree := &snapshot.Tree{
		Entries: []snapshot.TreeEntry{
			{Name: "file1.txt", Hash: "abc123", Mode: "100644"},
			{Name: "file2.txt", Hash: "def456", Mode: "100644"},
		},
	}

	var visited []string
	err := tw.Walk(tree, func(entry snapshot.TreeEntry) error {
		visited = append(visited, entry.Name)
		return nil
	})

	if err != nil {
		t.Errorf("Walk error: %v", err)
	}

	if len(visited) != 2 {
		t.Errorf("Visited = %v, want 2 entries", visited)
	}
}

func TestNewFileComparator(t *testing.T) {
	fc := NewFileComparator()
	if fc == nil {
		t.Fatal("NewFileComparator returned nil")
	}
}

func TestFileComparator_CompareContent(t *testing.T) {
	fc := NewFileComparator()

	same, err := fc.CompareContent([]byte("hello"), []byte("hello"))
	if err != nil {
		t.Errorf("CompareContent error: %v", err)
	}
	if !same {
		t.Error("Expected same content to be equal")
	}

	diff, err := fc.CompareContent([]byte("hello"), []byte("world"))
	if err != nil {
		t.Errorf("CompareContent error: %v", err)
	}
	if diff {
		t.Error("Expected different content to not be equal")
	}
}

func TestFileComparator_DiffLines(t *testing.T) {
	fc := NewFileComparator()

	our := []byte("line1\nline2\nline3")
	their := []byte("line1\nline2\nline3-mod")

	ourLines, theirLines := fc.DiffLines(our, their)

	if len(ourLines) != 3 {
		t.Errorf("Our lines count = %d, want 3", len(ourLines))
	}
	if len(theirLines) != 3 {
		t.Errorf("Their lines count = %d, want 3", len(theirLines))
	}
}

func TestTreeToMap(t *testing.T) {
	tree := &snapshot.Tree{
		Entries: []snapshot.TreeEntry{
			{Name: "a.txt", Hash: "hash1"},
			{Name: "b.txt", Hash: "hash2"},
			{Name: "subdir/", Hash: "hash3", Mode: "040000"},
		},
	}

	m := treeToMap(tree)

	if len(m) != 3 {
		t.Errorf("Map size = %d, want 3", len(m))
	}

	if m["a.txt"].Hash != "hash1" {
		t.Errorf("a.txt hash = %v, want hash1", m["a.txt"].Hash)
	}
	if m["b.txt"].Hash != "hash2" {
		t.Errorf("b.txt hash = %v, want hash2", m["b.txt"].Hash)
	}
}

func TestNewResolutionTracker(t *testing.T) {
	rt := NewResolutionTracker()
	if rt == nil {
		t.Fatal("NewResolutionTracker returned nil")
	}
	if rt.Count() != 0 {
		t.Errorf("Initial count = %d, want 0", rt.Count())
	}
}

func TestResolutionTracker_TrackResolution(t *testing.T) {
	rt := NewResolutionTracker()

	res := Resolution{
		Path:    "test.txt",
		Content: []byte("resolved"),
		Action:  "ours",
	}

	rt.TrackResolution("test.txt", res)

	if rt.Count() != 1 {
		t.Errorf("Count = %d, want 1", rt.Count())
	}

	if !rt.IsResolved("test.txt") {
		t.Error("Expected test.txt to be resolved")
	}
}

func TestResolutionTracker_GetResolution(t *testing.T) {
	rt := NewResolutionTracker()

	res := Resolution{
		Path:    "test.txt",
		Content: []byte("content"),
		Action:  "theirs",
	}

	rt.TrackResolution("test.txt", res)

	got, ok := rt.GetResolution("test.txt")
	if !ok {
		t.Error("Expected to get resolution")
	}
	if got.Action != "theirs" {
		t.Errorf("Action = %v, want theirs", got.Action)
	}
}

func TestResolutionTracker_GetStatus(t *testing.T) {
	rt := NewResolutionTracker()

	res := Resolution{Path: "test.txt", Action: "ours"}
	rt.TrackResolution("test.txt", res)

	status, ok := rt.GetStatus("test.txt")
	if !ok {
		t.Error("Expected to get status")
	}
	if status != StatusResolved {
		t.Errorf("Status = %v, want resolved", status)
	}
}

func TestResolutionTracker_GetResolved(t *testing.T) {
	rt := NewResolutionTracker()

	rt.TrackResolution("file1.txt", Resolution{Path: "file1.txt", Action: "ours"})
	rt.TrackResolution("file2.txt", Resolution{Path: "file2.txt", Action: "theirs"})

	resolved := rt.GetResolved()
	if len(resolved) != 2 {
		t.Errorf("Resolved count = %d, want 2", len(resolved))
	}
}
