// Deduplication Engine
// Purpose: Identifies and removes duplicate chunks to save storage space.

package rock

import (
	"log"
)

type DeduplicationEngine struct {
	ChunkDatabase   *ChunkDatabase
	ReferenceManager *ReferenceManager
}

func NewDeduplicationEngine(chunkDatabase *ChunkDatabase, referenceManager *ReferenceManager) *DeduplicationEngine {
	return &DeduplicationEngine{
		ChunkDatabase:   chunkDatabase,
		ReferenceManager: referenceManager,
	}
}

func (de *DeduplicationEngine) DeduplicateChunks(chunks []Chunk) ([]Chunk, error) {
	var deduplicatedChunks []Chunk
	seenHashes := make(map[string]bool)

	for _, chunk := range chunks {
		if seenHashes[chunk.Hash] {
			// Chunk is a duplicate, skip it
			continue
		}

		// Check if the chunk already exists in the database
		_, err := de.ChunkDatabase.GetChunk(chunk.Hash)
		if err == nil {
			// Chunk exists in the database, update reference count
			de.ReferenceManager.UpdateReferenceCount(chunk.Hash, 1)
			seenHashes[chunk.Hash] = true
			continue
		}

		// Insert the chunk into the database
		if err := de.ChunkDatabase.InsertChunk(chunk.Hash, chunk.Size, chunk.Data); err != nil {
			return nil, err
		}

		// Update reference count
		de.ReferenceManager.UpdateReferenceCount(chunk.Hash, 1)
		seenHashes[chunk.Hash] = true
		deduplicatedChunks = append(deduplicatedChunks, chunk)
	}

	return deduplicatedChunks, nil
}