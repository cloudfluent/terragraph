// Package privatefs protects engine-managed directories and files on every supported platform; it does not select paths or own artifact lifetimes.
package privatefs

import (
	"os"
	"path/filepath"
)

// Directory rejects replaceable parents and protects a real directory before any sensitive bytes can be written below it.
func Directory(path string) error {
	parent, err := openPlanDirectory(filepath.Dir(path), false)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	if err := checkPlanParent(parent); err != nil {
		return err
	}
	if err := createPlanDirectory(path); err != nil && !os.IsExist(err) {
		return err
	}
	dir, err := openPlanDirectory(path, true)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return protectPlanDirectory(dir)
}
