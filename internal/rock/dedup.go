package rock

import (
	"fmt"
)

// ReferenceManager manages chunk references for deduplication and garbage collection
type ReferenceManager struct {
	db *ChunkDatabase
}

// NewReferenceManager creates a new reference manager
func NewReferenceManager(db *ChunkDatabase) *ReferenceManager {
	return &ReferenceManager{db: db}
}

// AddChunks adds chunks for a file and updates reference counts
func (rm *ReferenceManager) AddChunks(filePath string, chunks []Chunk) error {
	return rm.db.AddFileChunks(filePath, chunks)
}

// GetRefs returns all chunk references for a file
func (rm *ReferenceManager) GetRefs(filePath string) ([]ChunkRef, error) {
	return rm.db.GetChunkRefs(filePath)
}

// GetRefCount returns the reference count for a chunk
func (rm *ReferenceManager) GetRefCount(hash string) (int, error) {
	return rm.db.GetRefCount(hash)
}

// GarbageCollect removes unreferenced chunks
func (rm *ReferenceManager) GarbageCollect() (int64, error) {
	return rm.db.DeleteUnreferencedChunks()
}

// DeduplicationEngine identifies and removes duplicate chunks
type DeduplicationEngine struct {
	chunker *FastCDC
	db      *ChunkDatabase
	refMgr  *ReferenceManager
}

// NewDeduplicationEngine creates a new deduplication engine
func NewDeduplicationEngine(db *ChunkDatabase) *DeduplicationEngine {
	return &DeduplicationEngine{
		chunker: DefaultFastCDC(),
		db:      db,
		refMgr:  NewReferenceManager(db),
	}
}

// ChunkFile chunks a file and stores unique chunks in the database
func (de *DeduplicationEngine) ChunkFile(filePath string) (int, int, error) {
	chunks, err := de.chunker.ChunkFile(filePath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to chunk file: %w", err)
	}

	// Add chunks to database (dedup happens at DB level)
	if err := de.refMgr.AddChunks(filePath, chunks); err != nil {
		return 0, 0, fmt.Errorf("failed to store chunks: %w", err)
	}

	// Get stats
	uniqueHashes := make(map[string]bool)
	for _, chunk := range chunks {
		uniqueHashes[chunk.Hash] = true
	}

	return len(chunks), len(uniqueHashes), nil
}

// ChunkData chunks raw data and stores unique chunks
func (de *DeduplicationEngine) ChunkData(filePath string, data []byte) (int, int, error) {
	chunks, err := de.chunker.Chunk(data)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to chunk data: %w", err)
	}

	// Add chunks to database
	if err := de.refMgr.AddChunks(filePath, chunks); err != nil {
		return 0, 0, fmt.Errorf("failed to store chunks: %w", err)
	}

	uniqueHashes := make(map[string]bool)
	for _, chunk := range chunks {
		uniqueHashes[chunk.Hash] = true
	}

	return len(chunks), len(uniqueHashes), nil
}

// GetStats returns deduplication statistics
func (de *DeduplicationEngine) GetStats() (totalChunks, uniqueChunks, totalSize, uniqueSize int64, err error) {
	chunks, err := de.db.GetUniqueChunks()
	if err != nil {
		return 0, 0, 0, 0, err
	}

	uniqueChunks = int64(len(chunks))
	totalChunks = uniqueChunks // Would need total refs for accurate count

	for _, chunk := range chunks {
		totalSize += chunk.Size
		uniqueSize += chunk.Size
	}

	return totalChunks, uniqueChunks, totalSize, uniqueSize, nil
}

// RunDeduplication analyzes files and identifies duplicates
func (de *DeduplicationEngine) RunDeduplication(filePaths []string) (DeduplicationReport, error) {
	var report DeduplicationReport

	for _, filePath := range filePaths {
		total, unique, err := de.ChunkFile(filePath)
		if err != nil {
			return report, err
		}
		report.FilesProcessed++
		report.TotalChunks += total
		report.UniqueChunks += unique
		report.SpaceSaved += int64(total - unique) // Approximation
	}

	// Get actual DB stats
	_, _, totalSize, uniqueSize, err := de.GetStats()
	if err != nil {
		return report, err
	}

	report.ActualSize = uniqueSize
	if totalSize > 0 {
		report.Ratio = float64(uniqueSize) / float64(totalSize)
	}

	return report, nil
}

// DeduplicationReport contains the results of deduplication
type DeduplicationReport struct {
	FilesProcessed int
	TotalChunks   int
	UniqueChunks  int
	SpaceSaved    int64
	ActualSize    int64
	Ratio         float64
}
