package repository

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

const maxLinkBytes = 65535

type linkContent struct {
	target    string
	directory bool
}

func validLinkTarget(target string) error {
	if target == "" || len(target) > maxLinkBytes || strings.ContainsRune(target, 0) {
		return fmt.Errorf("symbolic-link target is empty, contains NUL, or exceeds %d bytes", maxLinkBytes)
	}
	return nil
}
func readLink(path string) (_ linkContent, err error) {
	anchor, err := anchorPath(path, false)
	if err != nil {
		return linkContent{}, err
	}
	defer func() { err = errors.Join(err, anchor.close()) }()
	link, err := anchor.readLink()
	if err != nil {
		return linkContent{}, err
	}
	if err = validateMaterializableLink(link.target); err != nil {
		return linkContent{}, err
	}
	return link, nil
}
func readDirectory(path string) (_ []os.DirEntry, err error) {
	anchor, err := anchorPath(path, false)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, anchor.close()) }()
	f, err := anchor.openDirectory()
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not an ordinary directory: %s", path)
	}
	return f.ReadDir(-1)
}
