// Package pathidentity compares existing and prospective filesystem paths without writing files or choosing where callers store their data.
package pathidentity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Same preserves uncertainty when a missing path's directory does not expose its case rules, rather than treating every host as case-insensitive.
func Same(a, b string) (same, known bool, err error) {
	a, err = filepath.Abs(a)
	if err != nil {
		return false, false, fmt.Errorf("resolving path: %w", err)
	}
	b, err = filepath.Abs(b)
	if err != nil {
		return false, false, fmt.Errorf("resolving path: %w", err)
	}
	if a == b {
		return true, true, nil
	}
	parentA, tailA, infoA, err := existingAncestor(a)
	if err != nil {
		return false, false, err
	}
	_, tailB, infoB, err := existingAncestor(b)
	if err != nil {
		return false, false, err
	}
	if infoA == nil || infoB == nil {
		return false, false, nil
	}
	if !os.SameFile(infoA, infoB) {
		return false, true, nil
	}
	if tailA == tailB {
		return true, true, nil
	}
	if !strings.EqualFold(tailA, tailB) {
		return false, true, nil
	}
	insensitive, known, err := caseInsensitive(parentA)
	if err != nil {
		return false, false, fmt.Errorf("reading directory case rules for %s: %w", parentA, err)
	}
	return insensitive, known, nil
}

// existingAncestor retains missing suffixes so aliases through an existing symlinked directory are compared against the same filesystem object.
func existingAncestor(path string) (string, string, os.FileInfo, error) {
	tail := ""
	for {
		info, err := os.Stat(path)
		if err == nil {
			return path, tail, info, nil
		}
		if !os.IsNotExist(err) {
			return "", "", nil, fmt.Errorf("inspecting path %s: %w", path, err)
		}
		if link, err := os.Lstat(path); err == nil && link.Mode()&os.ModeSymlink != 0 {
			return "", "", nil, nil
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", "", nil, nil
		}
		tail = filepath.Join(filepath.Base(path), tail)
		path = parent
	}
}
