package rock

import (
	"fmt"
	"os"
	"path/filepath"
)

// ChunkGenerator generates chunks from binary files using FastCDC
type ChunkGenerator struct {
	chunker *FastCDC
	analyzer *FileAnalyzer
	db      *ChunkDatabase
	refMgr  *ReferenceManager
}

// NewChunkGenerator creates a new chunk generator
func NewChunkGenerator(db *ChunkDatabase) *ChunkGenerator {
	return &ChunkGenerator{
		chunker:  DefaultFastCDC(),
		analyzer: DefaultFileAnalyzer(),
		db:       db,
		refMgr:   NewReferenceManager(db),
	}
}

// ChunkResult contains the result of chunking a file
type ChunkResult struct {
	FilePath   string
	TotalChunks int
	UniqueChunks int
	FileSize    int64
	Error       error
}

// GenerateChunks chunks a single file
func (cg *ChunkGenerator) GenerateChunks(filePath string) (*ChunkResult, error) {
	// Analyze file first
	info, err := cg.analyzer.AnalyzeFile(filePath)
	if err != nil {
		return &ChunkResult{FilePath: filePath, Error: fmt.Errorf("analyze file: %w", err)}, nil
	}

	if !info.SuitableForChunking {
		return &ChunkResult{
			FilePath:    filePath,
			TotalChunks: 0,
			FileSize:    info.Size,
		}, nil
	}

	// Chunk the file
	chunks, err := cg.chunker.ChunkFile(filePath)
	if err != nil {
		return &ChunkResult{FilePath: filePath, Error: fmt.Errorf("chunk file: %w", err)}, nil
	}

	// Store in database with refs
	if err := cg.refMgr.AddChunks(filePath, chunks); err != nil {
		return &ChunkResult{FilePath: filePath, Error: fmt.Errorf("store chunks: %w", err)}, nil
	}

	// Count unique
	unique := make(map[string]bool)
	for _, c := range chunks {
		unique[c.Hash] = true
	}

	return &ChunkResult{
		FilePath:    filePath,
		TotalChunks: len(chunks),
		UniqueChunks: len(unique),
		FileSize:    info.Size,
	}, nil
}

// GenerateChunksFromDir chunks all suitable files in a directory
func (cg *ChunkGenerator) GenerateChunksFromDir(dirPath string, progress func(string, int, int)) ([]*ChunkResult, error) {
	var results []*ChunkResult

	// Get all suitable files
	infos, err := cg.analyzer.AnalyzeDirectory(dirPath)
	if err != nil {
		return nil, fmt.Errorf("analyze directory: %w", err)
	}

	suitable := cg.analyzer.GetSuitableFiles(infos)
	total := len(suitable)
	processed := 0

	for _, info := range suitable {
		result, err := cg.GenerateChunks(info.Path)
		if err != nil {
			result.Error = err
		}
		results = append(results, result)
		processed++

		if progress != nil {
			progress(info.Path, processed, total)
		}
	}

	return results, nil
}

// GenerateChunksFromPaths chunks multiple specific files
func (cg *ChunkGenerator) GenerateChunksFromPaths(paths []string, progress func(string, int, int)) ([]*ChunkResult, error) {
	var results []*ChunkResult
	total := len(paths)

	for i, path := range paths {
		// Resolve path
		absPath, err := filepath.Abs(path)
		if err != nil {
			results = append(results, &ChunkResult{
				FilePath: path,
				Error:    fmt.Errorf("resolve path: %w", err),
			})
			continue
		}

		// Check if directory or file
		info, err := os.Stat(absPath)
		if err != nil {
			results = append(results, &ChunkResult{
				FilePath: path,
				Error:    fmt.Errorf("stat: %w", err),
			})
			continue
		}

		if info.IsDir() {
			// Process directory
			dirResults, err := cg.GenerateChunksFromDir(absPath, nil)
			if err != nil {
				results = append(results, &ChunkResult{FilePath: path, Error: err})
			} else {
				// Combine results
				for _, r := range dirResults {
					results = append(results, r)
				}
			}
		} else {
			// Process single file
			result, _ := cg.GenerateChunks(absPath)
			results = append(results, result)
		}

		if progress != nil {
			progress(path, i+1, total)
		}
	}

	return results, nil
}

// ReconstructFile reconstructs a file from its chunks
func (cg *ChunkGenerator) ReconstructFile(filePath, outputPath string) error {
	refs, err := cg.refMgr.GetRefs(filePath)
	if err != nil {
		return fmt.Errorf("get refs: %w", err)
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	// Open output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	// Reconstruct each chunk
	for _, ref := range refs {
		_, content, err := cg.db.GetChunk(ref.ChunkHash)
		if err != nil {
			return fmt.Errorf("get chunk %s: %w", ref.ChunkHash[:8], err)
		}

		if _, err := outFile.Write(content); err != nil {
			return fmt.Errorf("write chunk: %w", err)
		}
	}

	return nil
}

// GetChunkStats returns statistics about chunks in the database
func (cg *ChunkGenerator) GetChunkStats() (int64, int64, int64, error) {
	totalChunks, totalSize, totalRefs, err := cg.db.GetStats()
	if err != nil {
		return 0, 0, 0, err
	}
	return totalChunks, totalSize, totalRefs, nil
}
