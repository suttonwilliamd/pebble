package repository

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func (a *pathAnchor) openDirectory() (*os.File, error) {
	h, err := relativeHandle(a.parent, a.leaf, windows.GENERIC_READ|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_DIRECTORY_FILE)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &info)
	if err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, errors.Join(fmt.Errorf("unsafe directory: %s", a.path), err, windows.CloseHandle(h))
	}
	return os.NewFile(uintptr(h), a.path), nil
}
