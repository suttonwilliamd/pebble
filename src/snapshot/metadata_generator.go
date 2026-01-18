// Metadata Generator
// Purpose: Creates metadata for each file, including timestamps, permissions, and other attributes.

package snapshot

import (
	"os"
	"path/filepath"
	"time"
)

type Metadata struct {
	Path         string
	Size         int64
	ModifiedTime time.Time
	CreatedTime  time.Time
	AccessedTime time.Time
	Permissions  string
	IsDirectory  bool
}

type MetadataGenerator struct {
}

func NewMetadataGenerator() *MetadataGenerator {
	return &MetadataGenerator{}
}

func (mg *MetadataGenerator) GenerateMetadata(fileIndex []FileMetadata) ([]Metadata, error) {
	var metadataList []Metadata

	for _, file := range fileIndex {
		filePath := filepath.Join(".", file.Path)

		fileInfo, err := os.Stat(filePath)
		if err != nil {
			return nil, err
		}

		metadata := Metadata{
			Path:         file.Path,
			Size:         file.Size,
			ModifiedTime: file.ModifiedTime,
			CreatedTime:  fileInfo.ModTime(),
			AccessedTime: fileInfo.ModTime(),
			Permissions:  fileInfo.Mode().String(),
			IsDirectory:  file.IsDirectory,
		}

		metadataList = append(metadataList, metadata)
	}

	return metadataList, nil
}

// GenerateMetadataForFile creates metadata for a single file (used by CLI)
func (mg *MetadataGenerator) GenerateMetadataForFile(path, hash string) (Metadata, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return Metadata{}, err
	}

	metadata := Metadata{
		Path:         path,
		Size:         fileInfo.Size(),
		ModifiedTime: fileInfo.ModTime(),
		CreatedTime:  fileInfo.ModTime(),
		AccessedTime: fileInfo.ModTime(),
		Permissions:  fileInfo.Mode().String(),
		IsDirectory:  fileInfo.IsDir(),
	}

	return metadata, nil
}
