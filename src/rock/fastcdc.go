package rock

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// Chunk represents a chunk of data
type Chunk struct {
	Hash string
	Data []byte
}

// FastCDC is responsible for content-defined chunking
type FastCDC struct {
	MinChunkSize int
	AvgChunkSize int
	MaxChunkSize int
}

// NewFastCDC creates a new FastCDC instance
func NewFastCDC(minChunkSize, avgChunkSize, maxChunkSize int) *FastCDC {
	return &FastCDC{
		MinChunkSize: minChunkSize,
		AvgChunkSize: avgChunkSize,
		MaxChunkSize: maxChunkSize,
	}
}

// ChunkFile chunks a file using the FastCDC algorithm
func (f *FastCDC) ChunkFile(filePath string) ([]Chunk, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var chunks []Chunk
	buffer := make([]byte, f.AvgChunkSize)
	for {
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			return nil, err
		}
		if n == 0 {
			break
		}

		// Generate hash for the chunk
		hash := sha256.New()
		hash.Write(buffer[:n])
		chunkHash := hex.EncodeToString(hash.Sum(nil))

		chunks = append(chunks, Chunk{
			Hash: chunkHash,
			Data: buffer[:n],
		})
	}

	return chunks, nil
}
