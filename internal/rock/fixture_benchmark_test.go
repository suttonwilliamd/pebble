package rock

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/testfixture"
)

// Corpus creation is excluded. These are current algorithm costs, not v2 claims.
func BenchmarkFixtureChunks(b *testing.B) {
	files, err := testfixture.Mixed(1 << 20)
	if err != nil {
		b.Fatal(err)
	}
	var revisions []testfixture.File
	for _, revision := range testfixture.Revisions(1 << 20) {
		revisions = append(revisions, revision.File)
	}
	groups := []struct {
		name  string
		files []testfixture.File
	}{{"mixed-1MiB", files}, {"revisions-1MiB", revisions}}
	for _, f := range files {
		if f.Path == "binary/random.bin" || f.Path == "binary/zeros.bin" || f.Path == "assets/scene.png" || f.Path == "assets/scene.jpg" || f.Path == "assets/bundle.zip" {
			groups = append(groups, struct {
				name  string
				files []testfixture.File
			}{f.Path, []testfixture.File{f}})
		}
	}
	for _, group := range groups {
		for _, stream := range []bool{false, true} {
			mode := "batch"
			if stream {
				mode = "stream"
			}
			b.Run(group.name+"/"+mode, func(b *testing.B) {
				chunker := DefaultFastCDC()
				var chunks int
				var uniqueBytes int64
				seen := map[string]bool{}
				for _, f := range group.files {
					cs, err := chunker.Chunk(f.Data)
					if err != nil {
						b.Fatal(err)
					}
					chunks += len(cs)
					for _, c := range cs {
						if !seen[c.Hash] {
							seen[c.Hash] = true
							uniqueBytes += c.Size
						}
					}
				}
				b.ReportAllocs()
				b.SetBytes(testfixture.TotalBytes(group.files))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					for _, f := range group.files {
						if stream {
							if err := chunker.ChunkStream(bytes.NewReader(f.Data), func(Chunk) error { return nil }); err != nil {
								b.Fatal(err)
							}
						} else {
							if _, err := chunker.Chunk(f.Data); err != nil {
								b.Fatal(err)
							}
						}
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(chunks), "chunks/op")
				b.ReportMetric(float64(uniqueBytes), "unique-B/op")
			})
		}
	}
}

// Setup/close/removal are excluded; the timer covers one durable AddFileChunks
// transaction with a fresh database. Payload generation/chunking are excluded.
func BenchmarkFixtureSQLiteIngest(b *testing.B) {
	data := testfixture.Noise(1 << 20)
	chunks, err := DefaultFastCDC().Chunk(data)
	if err != nil {
		b.Fatal(err)
	}
	root := b.TempDir()
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		dir, err := os.MkdirTemp(root, "ingest-")
		if err != nil {
			b.Fatal(err)
		}
		path := filepath.Join(dir, "chunks.db")
		db, err := NewChunkDatabase(path)
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		err = db.AddFileChunks("binary/random.bin", chunks)
		b.StopTimer()
		if err != nil {
			db.Close()
			b.Fatal(err)
		}
		_, stored, refs, err := db.GetStats()
		if err != nil {
			db.Close()
			b.Fatal(err)
		}
		if stored != int64(len(data)) || refs != int64(len(chunks)) {
			db.Close()
			b.Fatalf("ingest accounting: %d bytes %d refs", stored, refs)
		}
		if err := db.Close(); err != nil {
			b.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(info.Size()), "db-B/op")
		if err := os.RemoveAll(dir); err != nil {
			b.Fatal(err)
		}
	}
}
