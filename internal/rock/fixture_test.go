package rock

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

func TestFixtureReadAndCallbackFailures(t *testing.T) {
	data := testfixture.Noise(64 << 10)
	fault := errors.New("injected read failure")
	for _, offset := range []int{0, 1, 8192, 8193, 20000, len(data)} {
		t.Run(fmt.Sprint(offset), func(t *testing.T) {
			var emitted []byte
			err := DefaultFastCDC().ChunkStream(testfixture.FailAfter(data, offset, fault), func(c Chunk) error { emitted = append(emitted, c.Content...); return nil })
			if !errors.Is(err, fault) {
				t.Fatalf("read fault swallowed: %v", err)
			}
			if len(emitted) > offset || !bytes.Equal(emitted, data[:len(emitted)]) {
				t.Fatal("fault emitted fabricated/non-prefix bytes")
			}
			if offset > 8193 && len(emitted) == 0 {
				t.Fatal("fault did not exercise partial progress")
			}
		})
	}
	calls := 0
	err := DefaultFastCDC().ChunkStream(bytes.NewReader(data), func(Chunk) error {
		calls++
		if calls == 2 {
			return fault
		}
		return nil
	})
	if !errors.Is(err, fault) || calls != 2 {
		t.Fatalf("callback fault: calls=%d err=%v", calls, err)
	}
}

func TestFixtureSQLiteRollbackAfterPartialInsert(t *testing.T) {
	db, err := NewChunkDatabase(filepath.Join(t.TempDir(), "chunks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	chunker := DefaultFastCDC()
	seed, err := chunker.Chunk([]byte("previous durable reference"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddFileChunks("previous.bin", seed); err != nil {
		t.Fatal(err)
	}
	beforeRefs, err := db.GetChunkRefs("previous.bin")
	if err != nil {
		t.Fatal(err)
	}
	beforeChunks, beforeSize, beforeCount, err := db.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := chunker.Chunk(testfixture.Noise(64 << 10))
	if err != nil {
		t.Fatal(err)
	}
	// Trigger fails the second reference, after the first chunk and reference
	// have been inserted inside the transaction. No production hooks required.
	_, err = db.db.Exec(`CREATE TRIGGER fixture_fail BEFORE INSERT ON chunk_refs WHEN NEW.file_path = 'fault.bin' AND NEW.chunk_offset > 0 BEGIN SELECT RAISE(ABORT, 'PB02 injected reference failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddFileChunks("fault.bin", chunks); err == nil || !strings.Contains(err.Error(), "PB02 injected") {
		t.Fatalf("fault not reached: %v", err)
	}
	afterChunks, afterSize, afterCount, err := db.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if afterChunks != beforeChunks || afterSize != beforeSize || afterCount != beforeCount {
		t.Fatal("failed transaction leaked chunks or refs")
	}
	refs, err := db.GetChunkRefs("fault.bin")
	if err != nil || len(refs) != 0 {
		t.Fatalf("failed refs persisted: %v %v", refs, err)
	}
	refs, err = db.GetChunkRefs("previous.bin")
	if err != nil || !reflect.DeepEqual(refs, beforeRefs) {
		t.Fatalf("prior refs changed: %v %v", refs, err)
	}
	if _, err := db.db.Exec("DROP TRIGGER fixture_fail"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddFileChunks("fault.bin", chunks); err != nil {
		t.Fatal(err)
	}
	refs, err = db.GetChunkRefs("fault.bin")
	if err != nil {
		t.Fatal(err)
	}
	var restored []byte
	for _, ref := range refs {
		_, data, err := db.GetChunk(ref.ChunkHash)
		if err != nil {
			t.Fatal(err)
		}
		restored = append(restored, data...)
	}
	if !bytes.Equal(restored, testfixture.Noise(64<<10)) {
		t.Fatal("retry did not restore exact input")
	}
}

func TestFixtureMalformedSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.db")
	if err := os.WriteFile(path, []byte("SQLite format 3\x00truncated fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	db, err := NewChunkDatabase(path)
	if err == nil {
		db.Close()
		t.Fatal("malformed database accepted")
	}
}

func TestFixtureMissingChunkFailsReconstruction(t *testing.T) {
	root := t.TempDir()
	db, err := NewChunkDatabase(filepath.Join(root, "chunks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	chunks, err := DefaultFastCDC().Chunk(testfixture.Noise(16 << 10))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddFileChunks("missing.bin", chunks); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("DELETE FROM chunks WHERE hash = ?", chunks[1].Hash); err != nil {
		t.Fatal(err)
	}
	err = NewChunkGenerator(db).ReconstructFile("missing.bin", filepath.Join(root, "restored.bin"))
	if err == nil || !strings.Contains(err.Error(), chunks[1].Hash[:8]) {
		t.Fatalf("missing chunk not diagnosed: %v", err)
	}
}
