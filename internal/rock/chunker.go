package rock

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// Chunk represents a chunk of data with its hash
type Chunk struct {
	Hash    string
	Offset  int64
	Size    int64
	Content []byte
}

// Chunker performs content-defined chunking
type Chunker interface {
	Chunk(data []byte) ([]Chunk, error)
}

// FastCDC is a fast content-defined chunking algorithm
type FastCDC struct {
	MinChunkSize  int
	MaxChunkSize int
	WindowSize   int
	Mask         uint32
}

// NewFastCDC creates a new FastCDC chunker
func NewFastCDC(minChunk, maxChunk, window int) *FastCDC {
	// Calculate mask for chunk boundary detection
	// For maxChunk = 8192, we want roughly maxChunk/2 as average
	mask := uint32(1<<16 - 1) // Simple mask, adjust as needed
	
	return &FastCDC{
		MinChunkSize:  minChunk,
		MaxChunkSize:  maxChunk,
		WindowSize:    window,
		Mask:          mask,
	}
}

// DefaultFastCDC creates a chunker with sensible defaults
func DefaultFastCDC() *FastCDC {
	return NewFastCDC(512, 8192, 48)
}

// rollingHash computes a rolling hash (Rabin fingerprint variant)
func rollingHash(data []byte, window int) uint32 {
	var hash uint32
	for i := 0; i < window && i < len(data); i++ {
		hash = hash*31 + uint32(data[i])
	}
	return hash
}

// updateRollingHash updates the rolling hash
func updateRollingHash(oldHash uint32, outgoing, incoming byte, window int) uint32 {
	hash := oldHash*31 + uint32(incoming)
	// Remove outgoing byte's contribution (simplified)
	return hash
}

// Chunk performs content-defined chunking on the data
func (f *FastCDC) Chunk(data []byte) ([]Chunk, error) {
	if len(data) == 0 {
		return nil, nil
	}

	var chunks []Chunk
	offset := int64(0)
	pos := 0

	for pos < len(data) {
		// Determine chunk end
		end := pos + f.MinChunkSize
		if end > len(data) {
			end = len(data)
		}

		// Look for a chunk boundary in [min, max] range
		boundary := -1
		for i := pos + f.MinChunkSize; i < end && i < pos+f.MaxChunkSize; i++ {
			// Simple boundary detection: check for specific byte patterns
			// In a real implementation, we'd use the rolling hash
			if i+1 < len(data) {
				// Look for 0x00 followed by another byte (common in binary files)
				if data[i] == 0 && data[i+1] != 0 {
					boundary = i + 1
					break
				}
			}
		}

		// If no boundary found, use max chunk size
		if boundary == -1 {
			boundary = pos + f.MaxChunkSize
			if boundary > len(data) {
				boundary = len(data)
			}
		}

		chunkData := data[pos:boundary]
		
		// Compute hash
		hash := sha256.Sum256(chunkData)
		hashStr := hex.EncodeToString(hash[:])

		chunk := Chunk{
			Hash:    hashStr,
			Offset:  offset,
			Size:    int64(len(chunkData)),
			Content: chunkData,
		}

		chunks = append(chunks, chunk)
		offset += int64(len(chunkData))
		pos = boundary
	}

	return chunks, nil
}

// ChunkFile chunks a file
func (f *FastCDC) ChunkFile(path string) ([]Chunk, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return f.Chunk(data)
}

// ChunkReader chunks data from a reader
func (f *FastCDC) ChunkReader(reader io.Reader) ([]Chunk, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read: %w", err)
	}
	return f.Chunk(data)
}

// DeduplicateChunks identifies duplicate chunks
func DeduplicateChunks(chunks []Chunk) (map[string][]int, map[string]bool) {
	// Map from hash to chunk indices
	hashToIndices := make(map[string][]int)
	// Set of unique hashes
	uniqueHashes := make(map[string]bool)

	for i, chunk := range chunks {
		hashToIndices[chunk.Hash] = append(hashToIndices[chunk.Hash], i)
		uniqueHashes[chunk.Hash] = true
	}

	return hashToIndices, uniqueHashes
}

// ReconstructData reconstructs the original data from chunks
func ReconstructData(chunks []Chunk) []byte {
	var buf bytes.Buffer
	for _, chunk := range chunks {
		buf.Write(chunk.Content)
	}
	return buf.Bytes()
}

// ComputeDeduplicationRatio calculates the deduplication ratio
func ComputeDeduplicationRatio(chunks []Chunk) float64 {
	if len(chunks) == 0 {
		return 0
	}

	uniqueChunks := make(map[string]bool)
	totalSize := int64(0)
	uniqueSize := int64(0)

	for _, chunk := range chunks {
		totalSize += chunk.Size
		if !uniqueChunks[chunk.Hash] {
			uniqueChunks[chunk.Hash] = true
			uniqueSize += chunk.Size
		}
	}

	if totalSize == 0 {
		return 0
	}

	return float64(uniqueSize) / float64(totalSize)
}

// ChunkStats holds statistics about chunks
type ChunkStats struct {
	TotalChunks     int
	UniqueChunks    int
	TotalSize       int64
	UniqueSize      int64
	DeduplicationRatio float64
	AvgChunkSize    float64
	MinChunkSize    int64
	MaxChunkSize    int64
}

// ComputeStats computes statistics about chunks
func ComputeStats(chunks []Chunk) ChunkStats {
	if len(chunks) == 0 {
		return ChunkStats{}
	}

	uniqueChunks := make(map[string]bool)
	var totalSize, uniqueSize int64
	var minSize, maxSize int64 = -1, 0

	for _, chunk := range chunks {
		totalSize += chunk.Size
		uniqueSize += chunk.Size
		
		if chunk.Size < minSize || minSize == -1 {
			minSize = chunk.Size
		}
		if chunk.Size > maxSize {
			maxSize = chunk.Size
		}

		if !uniqueChunks[chunk.Hash] {
			uniqueChunks[chunk.Hash] = true
		} else {
			// Subtract duplicate size
			uniqueSize -= chunk.Size
		}
	}

	avgSize := float64(totalSize) / float64(len(chunks))

	return ChunkStats{
		TotalChunks:        len(chunks),
		UniqueChunks:      len(uniqueChunks),
		TotalSize:         totalSize,
		UniqueSize:        uniqueSize,
		DeduplicationRatio: float64(uniqueSize) / float64(totalSize),
		AvgChunkSize:      avgSize,
		MinChunkSize:      minSize,
		MaxChunkSize:      maxSize,
	}
}
