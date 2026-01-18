package snapshot

// DeltaSnapshot represents a delta snapshot
type DeltaSnapshot struct {
	BaseSnapshotHash string
	AddedFiles       []FileIndex
	ModifiedFiles    []FileIndex
	DeletedFiles     []FileIndex
}

// DeltaSnapshotCreator is responsible for creating delta snapshots
type DeltaSnapshotCreator struct {
}

// NewDeltaSnapshotCreator creates a new DeltaSnapshotCreator instance
func NewDeltaSnapshotCreator() *DeltaSnapshotCreator {
	return &DeltaSnapshotCreator{}
}

// CreateDeltaSnapshot creates a delta snapshot from two full snapshots
func (dsc *DeltaSnapshotCreator) CreateDeltaSnapshot(baseSnapshot, newSnapshot []FileIndex) DeltaSnapshot {
	var deltaSnapshot DeltaSnapshot

	// Create a map of files in the base snapshot for easy lookup
	baseSnapshotFiles := make(map[string]FileIndex)
	for _, file := range baseSnapshot {
		baseSnapshotFiles[file.Path] = file
	}

	// Identify added and modified files
	for _, file := range newSnapshot {
		if existingFile, exists := baseSnapshotFiles[file.Path]; exists {
			if existingFile.Hash != file.Hash {
				deltaSnapshot.ModifiedFiles = append(deltaSnapshot.ModifiedFiles, file)
			}
		} else {
			deltaSnapshot.AddedFiles = append(deltaSnapshot.AddedFiles, file)
		}
	}

	// Identify deleted files
	for _, file := range baseSnapshot {
		found := false
		for _, newFile := range newSnapshot {
			if file.Path == newFile.Path {
				found = true
				break
			}
		}
		if !found {
			deltaSnapshot.DeletedFiles = append(deltaSnapshot.DeletedFiles, file)
		}
	}

	return deltaSnapshot
}
