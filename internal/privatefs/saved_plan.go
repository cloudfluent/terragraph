package privatefs

import (
	"fmt"
	"os"
	"path/filepath"
)

// prepareSavedPlan protects the directory before Terraform can create sensitive bytes and never follows a leftover plan into another file.
func Prepare(path string) (func(), error) {
	dir := filepath.Dir(path)
	parent, err := openPlanDirectory(filepath.Dir(dir), false)
	if err != nil {
		return nil, fmt.Errorf("opening plan parent: %w", err)
	}
	defer func() { _ = parent.Close() }()
	if err := checkPlanParent(parent); err != nil {
		return nil, err
	}
	if err := createPlanDirectory(dir); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("creating plan directory: %w", err)
	}
	plans, err := openPlanDirectory(dir, true)
	if err != nil {
		return nil, fmt.Errorf("opening plan directory: %w", err)
	}
	defer func() { _ = plans.Close() }()
	if err := protectPlanDirectory(plans); err != nil {
		return nil, fmt.Errorf("protecting plan directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("saved plan %s is not a regular file; remove it before applying", path)
		}
		// Unlink rather than truncate or chmod, because an old regular plan could be a hard link to a file this run does not own.
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("removing stale plan: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("checking saved plan: %w", err)
	}
	f, err := createPreparedPlan(path)
	if err != nil {
		return nil, fmt.Errorf("preparing saved plan: %w", err)
	}
	cleanup := func() { _ = os.Remove(path) }
	if err := f.Close(); err != nil {
		cleanup()
		return nil, fmt.Errorf("closing prepared plan: %w", err)
	}
	return cleanup, nil
}
