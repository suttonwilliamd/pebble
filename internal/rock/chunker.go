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
	Prime        uint32
	Target       uint32
}

// NewFastCDC creates a new FastCDC chunker
func NewFastCDC(minChunk, maxChunk, window int) *FastCDC {
	// FastCDC parameters
	prime := uint32(2083806171) // Large prime for polynomial hash
	mask := uint32(0x3FFF)       // 14-bit mask - ~1/16K chance per position
	target := uint32(0x1000)      // Specific target value to reduce false positives
	
	return &FastCDC{
		MinChunkSize: minChunk,
		MaxChunkSize: maxChunk,
		WindowSize:   window,
		Mask:         mask,
		Prime:        prime,
		Target:       target,
	}
}

// DefaultFastCDC creates a chunker with sensible defaults
func DefaultFastCDC() *FastCDC {
	return NewFastCDC(512, 8192, 48)
}

// computeHash computes the polynomial rolling hash for a chunk
func computeHash(data []byte, prime uint32) uint32 {
	var hash uint32
	for _, b := range data {
		hash = hash*prime + uint32(b)
	}
	return hash
}

// FastCDC Chunk performs content-defined chunking using Rabin fingerprint
func (f *FastCDC) Chunk(data []byte) ([]Chunk, error) {
	if len(data) == 0 {
		return nil, nil
	}

	var chunks []Chunk
	offset := int64(0)
	pos := 0

	// Pre-compute prime^window for rolling hash update
	primePow := uint32(1)
	for i := 0; i < f.WindowSize; i++ {
		primePow *= f.Prime
	}

	for pos < len(data) {
		// Need at least min chunk size + window to start looking for boundaries
		if pos+f.WindowSize >= len(data) {
			// Just take remaining data as final chunk
			chunkData := data[pos:]
			contentHash := sha256.Sum256(chunkData)
			chunk := Chunk{
				Hash:    hex.EncodeToString(contentHash[:]),
				Offset:  offset,
				Size:    int64(len(chunkData)),
				Content: make([]byte, len(chunkData)),
			}
			copy(chunk.Content, chunkData)
			chunks = append(chunks, chunk)
			break
		}
		
		// Compute initial hash for the first window
		windowEnd := pos + f.WindowSize
		hash := computeHash(data[pos:windowEnd], f.Prime)
		
		// Slide the window and check for boundaries
		chunkEnd := -1
		avgChunkSizeVal := (f.MinChunkSize + f.MaxChunkSize) / 2
		
		for i := windowEnd; i < len(data) && i < pos+f.MaxChunkSize; i++ {
			// Update rolling hash FIRST (so we have hash for current window position)
			hash = hash*f.Prime + uint32(data[i]) - primePow*uint32(data[i-f.WindowSize])
			
			// Check for chunk boundary once we've passed min size
			if (i - pos) >= f.MinChunkSize {
				// Primary: rolling hash boundary
				if (hash & f.Mask) == f.Target {
					chunkEnd = i
					break
				}
				// Secondary: force boundary at average chunk size if no boundary found yet
				if chunkEnd == -1 && (i-pos) >= avgChunkSizeVal {
					// Look ahead for a zero byte (common boundary in binary)
					for j := i; j < i+64 && j < len(data) && j < pos+f.MaxChunkSize; j++ {
						if data[j] == 0 && j+1 < len(data) && data[j+1] != 0 {
							chunkEnd = j + 1
							break
						}
					}
					if chunkEnd != -1 {
						break
					}
				}
			}
		}

		// If no boundary found, use max chunk size (but respect data bounds)
		if chunkEnd == -1 {
			chunkEnd = pos + f.MaxChunkSize
			if chunkEnd > len(data) {
				chunkEnd = len(data)
			}
		}

		chunkData := data[pos:chunkEnd]
		
		// Compute content hash (SHA-256 for deduplication)
		contentHash := sha256.Sum256(chunkData)
		hashStr := hex.EncodeToString(contentHash[:])

		chunk := Chunk{
			Hash:    hashStr,
			Offset:  offset,
			Size:    int64(len(chunkData)),
			Content: make([]byte, len(chunkData)),
		}
		copy(chunk.Content, chunkData)

		chunks = append(chunks, chunk)
		offset += int64(len(chunkData))
		pos = chunkEnd
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

// ChunkStream chunks data from a stream (memory efficient)
func (f *FastCDC) ChunkStream(reader io.Reader, chunkCallback func(Chunk) error) error {
	const bufSize = 64 * 1024 // 64KB buffer
	buf := make([]byte, bufSize+f.WindowSize) // Extra window for rolling hash
	
	offset := int64(0)
	pos := 0
	
	for {
		n, err := reader.Read(buf[pos:])
		if n == 0 && err == io.EOF {
			break
		}
		if err != nil && err != io.EOF {
			return fmt.Errorf("read error: %w", err)
		}
		
		total := pos + n
		
		// Process chunks from buffer
		chunkPos := 0
		for chunkPos < total-f.WindowSize {
			// Find chunk boundary
			windowEnd := chunkPos + f.WindowSize
			hash := computeHash(buf[chunkPos:windowEnd], f.Prime)
			
			chunkEnd := -1
			for i := windowEnd; i < total && int64(i) < int64(chunkPos)+int64(f.MaxChunkSize); i++ {
				if (i-chunkPos) >= f.MinChunkSize && (hash&f.Mask) == f.Target {
					chunkEnd = i
					break
				}
				hash = hash*f.Prime + uint32(buf[i]) - uint32(buf[i-f.WindowSize])*f.Prime
			}
			
			if chunkEnd == -1 {
				chunkEnd = chunkPos + f.MaxChunkSize
				if chunkEnd > total-f.WindowSize {
					// Not enough data for full chunk, save for next iteration
					break
				}
			}
			
			chunkData := buf[chunkPos:chunkEnd]
			contentHash := sha256.Sum256(chunkData)
			
			chunk := Chunk{
				Hash:    hex.EncodeToString(contentHash[:]),
				Offset:  offset,
				Size:    int64(len(chunkData)),
				Content: make([]byte, len(chunkData)),
			}
			copy(chunk.Content, chunkData)
			
			if err := chunkCallback(chunk); err != nil {
				return err
			}
			
			offset += int64(len(chunkData))
			chunkPos = chunkEnd
		}
		
		// Keep remaining data in buffer
		remaining := total - chunkPos
		if remaining > 0 {
			copy(buf[:remaining], buf[chunkPos:total])
		}
		pos = remaining
		
		if err == io.EOF {
			break
		}
	}
	
	return nil
}

// DeduplicateChunks identifies duplicate chunks
func DeduplicateChunks(chunks []Chunk) (map[string][]int, map[string]bool) {
	hashToIndices := make(map[string][]int)
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
	TotalChunks         int
	UniqueChunks       int
	TotalSize          int64
	UniqueSize         int64
	DeduplicationRatio float64
	AvgChunkSize       float64
	MinChunkSize       int64
	MaxChunkSize       int64
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
		
		if chunk.Size < minSize || minSize == -1 {
			minSize = chunk.Size
		}
		if chunk.Size > maxSize {
			maxSize = chunk.Size
		}

		if !uniqueChunks[chunk.Hash] {
			uniqueChunks[chunk.Hash] = true
			uniqueSize += chunk.Size
		}
	}

	avgSize := float64(totalSize) / float64(len(chunks))

	return ChunkStats{
		TotalChunks:         len(chunks),
		UniqueChunks:        len(uniqueChunks),
		TotalSize:           totalSize,
		UniqueSize:          uniqueSize,
		DeduplicationRatio:  float64(uniqueSize) / float64(totalSize),
		AvgChunkSize:        avgSize,
		MinChunkSize:        minSize,
		MaxChunkSize:        maxSize,
	}
}
