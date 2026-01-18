// Snapshot Comparator
// Purpose: Compares snapshots to identify differences.

package combine

import (
	"fmt"

	"pebble/src/snapshot"
)

type SnapshotDifference struct {
	FilePath string
	Type     string // "added", "modified", "deleted"
}

type SnapshotComparator struct {
	ObjectDatabase *snapshot.ObjectDatabase
}

func NewSnapshotComparator(objectDatabase *snapshot.ObjectDatabase) *SnapshotComparator {
	return &SnapshotComparator{ObjectDatabase: objectDatabase}
}

func (sc *SnapshotComparator) CompareSnapshots(snapshotID1, snapshotID2 int) ([]SnapshotDifference, error) {
	// Get objects for both snapshots
	objects1, err := sc.ObjectDatabase.GetSnapshotObjects(snapshotID1)
	if err != nil {
		return nil, err
	}

	objects2, err := sc.ObjectDatabase.GetSnapshotObjects(snapshotID2)
	if err != nil {
		return nil, err
	}

	// Create a map of objects in snapshot 2 for quick lookup
	objects2Map := make(map[string]snapshot.SnapshotObject)
	for _, obj := range objects2 {
		objects2Map[obj.Path] = obj
	}

	var differences []SnapshotDifference

	// Check for added or modified files
	for _, obj1 := range objects1 {
		obj2, exists := objects2Map[obj1.Path]
		if !exists {
			differences = append(differences, SnapshotDifference{
				FilePath: obj1.Path,
				Type:     "deleted",
			})
		} else if obj1.ObjectHash != obj2.ObjectHash {
			differences = append(differences, SnapshotDifference{
				FilePath: obj1.Path,
				Type:     "modified",
			})
		}
	}

	// Check for added files in snapshot 2
	for _, obj2 := range objects2 {
		_, exists := objects2Map[obj2.Path]
		if !exists {
			differences = append(differences, SnapshotDifference{
				FilePath: obj2.Path,
				Type:     "added",
			})
		}
	}

	return differences, nil
}