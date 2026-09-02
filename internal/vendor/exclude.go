package vendor

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// alwaysExcluded is pruned from every vendored fetch regardless of configuration: a nested .git directory would otherwise make the outer repository treat the vendored copy as an embedded git repo, and it isn't something anyone vendoring a module would ever want to keep.
var alwaysExcluded = []string{".git"}

// prune removes everything under dir matching patterns (plus alwaysExcluded, unconditionally). See matchExclude for pattern semantics.
func prune(dir string, patterns []string) error {
	all := make([]string, 0, len(alwaysExcluded)+len(patterns))
	all = append(all, alwaysExcluded...)
	all = append(all, patterns...)

	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		if matchExclude(all, rel) {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return nil
	})
}

// matchExclude reports whether relPath (slash-separated, relative to the vendored root) matches any pattern. A pattern containing "/" is anchored to the full relative path; a pattern with no "/" matches the basename at any depth, the same two-mode split .gitignore uses for anchored vs. bare patterns. Unlike real .gitignore, "**" is not recursive here: a multi-segment pattern only matches that exact path shape.
func matchExclude(patterns []string, relPath string) bool {
	base := filepath.Base(relPath)
	for _, p := range patterns {
		if strings.Contains(p, "/") {
			if ok, _ := filepath.Match(p, relPath); ok {
				return true
			}
		} else {
			if ok, _ := filepath.Match(p, base); ok {
				return true
			}
		}
	}
	return false
}
