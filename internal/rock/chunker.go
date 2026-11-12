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
	MinChunkSize int
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
	mask := uint32(0x3FFF)      // 14-bit mask - ~1/16K chance per position
	target := uint32(0x1000)    // Specific target value to reduce false positives

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
func (f *FastCDC) validate() error {
	if f.MinChunkSize <= 0 || f.MaxChunkSize < f.MinChunkSize || f.WindowSize <= 0 || f.WindowSize > f.MinChunkSize || f.MaxChunkSize == int(^uint(0)>>1) {
		return fmt.Errorf("invalid chunker sizes: require 0 < window <= min <= max < MaxInt")
	}
	return nil
}

func (f *FastCDC) Chunk(data []byte) ([]Chunk, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	var chunks []Chunk
	var offset int64
	primePow := f.primePower()
	for len(data) != 0 {
		end := f.boundary(data, primePow)
		content := bytes.Clone(data[:end])
		sum := sha256.Sum256(content)
		chunks = append(chunks, Chunk{
			Hash: hex.EncodeToString(sum[:]), Offset: offset,
			Size: int64(end), Content: content,
		})
		offset += int64(end)
		data = data[end:]
	}
	return chunks, nil
}

func (f *FastCDC) primePower() uint32 {
	power := uint32(1)
	for i := 0; i < f.WindowSize; i++ {
		power *= f.Prime
	}
	return power
}

// boundary needs at most MaxChunkSize+1 bytes. The extra byte preserves the
// zero/nonzero lookahead at the maximum boundary, even across reader fragments.
func (f *FastCDC) boundary(data []byte, primePow uint32) int {
	if len(data) <= f.WindowSize {
		return len(data)
	}
	hash := computeHash(data[:f.WindowSize], f.Prime)
	average := f.MinChunkSize + (f.MaxChunkSize-f.MinChunkSize)/2
	for i := f.WindowSize; i < len(data) && i < f.MaxChunkSize; i++ {
		hash = hash*f.Prime + uint32(data[i]) - primePow*uint32(data[i-f.WindowSize])
		if i < f.MinChunkSize {
			continue
		}
		if hash&f.Mask == f.Target {
			return i
		}
		if i >= average {
			for j := i; j-i < 64 && j < len(data) && j < f.MaxChunkSize; j++ {
				if data[j] == 0 && j+1 < len(data) && data[j+1] != 0 {
					return j + 1
				}
			}
		}
	}
	if len(data) < f.MaxChunkSize {
		return len(data)
	}
	return f.MaxChunkSize
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

// ChunkStream uses bounded memory and exactly the same boundaries as Chunk.
// Callback chunks own their contents and may be retained. Use NewChunkReader
// instead when the consumer can finish with each borrowed chunk before Next.
func (f *FastCDC) ChunkStream(reader io.Reader, chunkCallback func(Chunk) error) error {
	if chunkCallback == nil {
		return fmt.Errorf("nil chunk callback")
	}
	stream, err := f.NewChunkReader(reader)
	if err != nil {
		return err
	}
	var offset int64
	for {
		data, err := stream.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		content := bytes.Clone(data)
		sum := sha256.Sum256(content)
		c := Chunk{Hash: hex.EncodeToString(sum[:]), Offset: offset, Size: int64(len(content)), Content: content}
		if err := chunkCallback(c); err != nil {
			return err
		}
		offset += c.Size
	}
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
	TotalChunks        int
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
		TotalChunks:        len(chunks),
		UniqueChunks:       len(uniqueChunks),
		TotalSize:          totalSize,
		UniqueSize:         uniqueSize,
		DeduplicationRatio: float64(uniqueSize) / float64(totalSize),
		AvgChunkSize:       avgSize,
		MinChunkSize:       minSize,
		MaxChunkSize:       maxSize,
	}
}
