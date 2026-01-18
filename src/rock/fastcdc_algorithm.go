// FastCDC Algorithm
// Purpose: Implements the FastCDC algorithm for content-defined chunking.

package rock

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

type Chunk struct {
	Hash string
	Size int
	Data []byte
}

type FastCDCAlgorithm struct {
	MinChunkSize int
	AvgChunkSize int
	MaxChunkSize int
}

func NewFastCDCAlgorithm(minChunkSize, avgChunkSize, maxChunkSize int) *FastCDCAlgorithm {
	return &FastCDCAlgorithm{
		MinChunkSize: minChunkSize,
		AvgChunkSize: avgChunkSize,
		MaxChunkSize: maxChunkSize,
	}
}

func (f *FastCDCAlgorithm) ChunkFile(filePath string) ([]Chunk, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var chunks []Chunk
	buffer := make([]byte, f.MaxChunkSize)

	for {
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			return nil, err
		}
		if n == 0 {
			break
		}

		chunkData := buffer[:n]
		if len(chunkData) >= f.MinChunkSize {
			if f.isChunkBoundary(chunkData) {
				chunkHash := sha256.Sum256(chunkData)
				chunks = append(chunks, Chunk{
					Hash: hex.EncodeToString(chunkHash[:]),
					Size: len(chunkData),
					Data: chunkData,
				})
			}
		}
	}

	return chunks, nil
}

func (f *FastCDCAlgorithm) isChunkBoundary(data []byte) bool {
	hash := sha256.Sum256(data)
	hashInt := int(hash[0]) | int(hash[1])<<8 | int(hash[2])<<16 | int(hash[3])<<24
	target := (hashInt % (f.MaxChunkSize - f.MinChunkSize)) + f.MinChunkSize
	return hashInt%f.AvgChunkSize < target
}