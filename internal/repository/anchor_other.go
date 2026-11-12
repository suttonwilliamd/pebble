//go:build !windows && !linux && !darwin

package repository

import (
	"fmt"
	"os"
)

func renameAnchoredExclusive(_ int, _ string, _ int, _ string) error {
	return fmt.Errorf("anchored exclusive rename unsupported on this platform")
}
func (a *pathAnchor) openLink() (*os.File, error) {
	return nil, fmt.Errorf("anchored symbolic-link identity handles unsupported on this platform")
}
