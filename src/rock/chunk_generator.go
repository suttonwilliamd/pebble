// Chunk Generator
// Purpose: Generates chunks from binary files using the FastCDC algorithm.

package rock

import (
	"os"
)

type ChunkGenerator struct {
	FileAnalyzer    *FileAnalyzer
	FastCDCAlgorithm *FastCDCAlgorithm
}

func NewChunkGenerator(fileAnalyzer *FileAnalyzer, fastCDCAlgorithm *FastCDCAlgorithm) *ChunkGenerator {
	return &ChunkGenerator{
		FileAnalyzer:    fileAnalyzer,
		FastCDCAlgorithm: fastCDCAlgorithm,
	}
}

func (cg *ChunkGenerator) GenerateChunks(filePath string) ([]Chunk, error) {
	// Analyze the file
	analysis, err := cg.FileAnalyzer.AnalyzeFile(filePath)
	if err != nil {
		return nil, err
	}

	if !analysis.IsSuitable {
		return nil, nil
	}

	// Chunk the file
	chunks, err := cg.FastCDCAlgorithm.ChunkFile(filePath)
	if err != nil {
		return nil, err
	}

	return chunks, nil
}