package snapshot

// DeltaSnapshotRetriever is responsible for retrieving a full snapshot from a base snapshot and a delta snapshot
type DeltaSnapshotRetriever struct {
}

// NewDeltaSnapshotRetriever creates a new DeltaSnapshotRetriever instance
func NewDeltaSnapshotRetriever() *DeltaSnapshotRetriever {
	return &DeltaSnapshotRetriever{}
}

// RetrieveFullSnapshot retrieves a full snapshot from a base snapshot and a delta snapshot
func (dsr *DeltaSnapshotRetriever) RetrieveFullSnapshot(baseSnapshot []FileIndex, deltaSnapshot DeltaSnapshot) []FileIndex {
	// Create a map of files in the base snapshot for easy lookup
	baseSnapshotFiles := make(map[string]FileIndex)
	for _, file := range baseSnapshot {
		baseSnapshotFiles[file.Path] = file
	}

	// Apply additions and modifications
	for _, file := range deltaSnapshot.AddedFiles {
		baseSnapshotFiles[file.Path] = file
	}

	for _, file := range deltaSnapshot.ModifiedFiles {
		baseSnapshotFiles[file.Path] = file
	}

	// Apply deletions
	for _, file := range deltaSnapshot.DeletedFiles {
		delete(baseSnapshotFiles, file.Path)
	}

	// Convert the map back to a slice
	var fullSnapshot []FileIndex
	for _, file := range baseSnapshotFiles {
		fullSnapshot = append(fullSnapshot, file)
	}

	return fullSnapshot
}
