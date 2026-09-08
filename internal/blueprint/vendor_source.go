package blueprint

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// VendoredSourceFilename identifies the engine-owned package layout without changing existing manifest entries or legacy flat directories.
const VendoredSourceFilename = ".terragraph-source.json"

// VendoredSource retains the execution subdirectory inside a fetched package so relative child-module paths keep their original meaning.
type VendoredSource struct {
	Subdir string `json:"subdir"`
}

// Directory rejects lexical and symlink escapes before a package's metadata can select code outside the vendored tree.
func (s VendoredSource) Directory(root string) (string, error) {
	if s.Subdir == "" || !fs.ValidPath(s.Subdir) || strings.Contains(s.Subdir, `\`) || isAbsoluteAnywhere(s.Subdir) {
		return "", fmt.Errorf("subdir %q must be a normalized relative path within the source package; re-run terragraph vendor --force", s.Subdir)
	}
	dir := filepath.Join(root, filepath.FromSlash(s.Subdir))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolving source package: %w", err)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolving source subdir %q; re-run terragraph vendor --force: %w", s.Subdir, err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedDir)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("subdir %q resolves outside the source package; remove the escaping symlink and re-run terragraph vendor --force", s.Subdir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("inspecting source subdir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("source subdir %q is not a directory; select a module directory", s.Subdir)
	}
	return dir, nil
}

// ReadVendoredSource keeps absent legacy metadata distinct from malformed package metadata, which must never silently change the execution directory.
func ReadVendoredSource(root string) (*VendoredSource, error) {
	marker := filepath.Join(root, VendoredSourceFilename)
	data, err := os.ReadFile(marker)
	if os.IsNotExist(err) {
		if _, statErr := os.Lstat(marker); os.IsNotExist(statErr) {
			return nil, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("reading vendored source metadata: %w", err)
	}
	var source VendoredSource
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, fmt.Errorf("invalid vendored source metadata; re-run terragraph vendor --force: %w", err)
	}
	return &source, nil
}
