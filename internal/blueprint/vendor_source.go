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

// vendoredSourceRecovery keeps graph diagnostics actionable because --force also refuses metadata it cannot inspect safely.
const vendoredSourceRecovery = "verify local state and backups; recover or migrate any affected state outside the vendored copy before removing that copy and running terragraph vendor again"

// VendoredSource retains the execution subdirectory inside a fetched package so relative child-module paths keep their original meaning.
type VendoredSource struct {
	Subdir string `json:"subdir"`
}

// Directory rejects lexical and symlink escapes before a package's metadata can select code outside the vendored tree.
func (s VendoredSource) Directory(root string) (string, error) {
	if s.Subdir == "" || !fs.ValidPath(s.Subdir) || strings.Contains(s.Subdir, `\`) || isAbsoluteAnywhere(s.Subdir) {
		return "", fmt.Errorf("subdir %q must be a normalized relative path within the source package; %s", s.Subdir, vendoredSourceRecovery)
	}
	dir := filepath.Join(root, filepath.FromSlash(s.Subdir))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolving source package; %s: %w", vendoredSourceRecovery, err)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolving source subdir %q; %s: %w", s.Subdir, vendoredSourceRecovery, err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedDir)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("subdir %q resolves outside the source package; %s", s.Subdir, vendoredSourceRecovery)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("inspecting source subdir; %s: %w", vendoredSourceRecovery, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("source subdir %q is not a directory; %s", s.Subdir, vendoredSourceRecovery)
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
		return nil, fmt.Errorf("reading vendored source metadata; %s: %w", vendoredSourceRecovery, err)
	}
	var source VendoredSource
	if err := json.Unmarshal(data, &source); err != nil {
		return nil, fmt.Errorf("invalid vendored source metadata; %s: %w", vendoredSourceRecovery, err)
	}
	return &source, nil
}
