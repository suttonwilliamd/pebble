// File Generator
// Purpose: Generates conflict files with `.theirs` suffixes.

package combine

import (
	"io"
	"os"
	"path/filepath"
)

type FileGenerator struct {
}

func NewFileGenerator() *FileGenerator {
	return &FileGenerator{}
}

func (fg *FileGenerator) GenerateConflictFiles(conflicts []FileConflict) error {
	for _, conflict := range conflicts {
		if conflict.Type == "modified" {
			// Generate .theirs file
			theirsPath := conflict.FilePath + ".theirs"
			if err := fg.copyFile(conflict.FilePath, theirsPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func (fg *FileGenerator) copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}