package vendor

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	getter "github.com/hashicorp/go-getter/v2"
)

// gitFetcher vendors git sources via go-getter, which already implements address detection/cloning for the forms Terraform's own module.source supports (git::..., github.com/org/repo, git@host:path, etc.).
type gitFetcher struct{}

// gitAddressHints are checked before attempting an actual fetch, purely so an unsupported source shape (e.g. a future Terraform Registry address, not yet implemented) fails with a clear "no fetcher recognizes this source" instead of a confusing error from deep inside go-getter.
var gitAddressHints = []string{"git::", "git@", "github.com/", "gitlab.com/", "bitbucket.org/"}

func (gitFetcher) Matches(src string) bool {
	if strings.HasSuffix(src, ".git") {
		return true
	}
	for _, hint := range gitAddressHints {
		if strings.HasPrefix(src, hint) {
			return true
		}
	}
	return false
}

func (gitFetcher) Fetch(ctx context.Context, src, dst string) error {
	packageSource, subdir := getter.SourceDirSubdir(src)
	if subdir != "" && (!fs.ValidPath(subdir) || strings.Contains(subdir, `\`)) {
		return fmt.Errorf("source subdir %q must stay within its package", subdir)
	}
	if _, err := getter.Get(ctx, dst, packageSource); err != nil {
		return err
	}
	marker := filepath.Join(dst, blueprint.VendoredSourceFilename)
	if _, err := os.Lstat(marker); err == nil {
		return fmt.Errorf("source package contains reserved file %s; remove or rename it upstream", blueprint.VendoredSourceFilename)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking source package metadata: %w", err)
	}
	if subdir == "" {
		return nil
	}
	selected, err := getter.SubdirGlob(dst, subdir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(dst, selected)
	if err != nil {
		return fmt.Errorf("resolving source subdir: %w", err)
	}
	source := blueprint.VendoredSource{Subdir: filepath.ToSlash(rel)}
	if _, err := source.Directory(dst); err != nil {
		return err
	}
	data, err := json.Marshal(source)
	if err != nil {
		return fmt.Errorf("encoding source metadata: %w", err)
	}
	if err := os.WriteFile(marker, data, 0o644); err != nil {
		return fmt.Errorf("writing source metadata: %w", err)
	}
	return nil
}

// needsPackageLayout upgrades only legacy subdirectory copies; root sources without a manifest keep their existing no-fetch behavior.
func needsPackageLayout(src, dir string) bool {
	_, subdir := getter.SourceDirSubdir(src)
	if subdir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, blueprint.VendoredSourceFilename))
	return os.IsNotExist(err)
}
