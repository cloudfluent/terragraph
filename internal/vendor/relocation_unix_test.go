//go:build !windows

package vendor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAll_LegacySubdirWithCaseAliasedStateRefusesUpgrade(t *testing.T) {
	n, baseDir, dir := legacySubdirFixture(t, "terraform {\n backend \"local\" {}\n}")
	alias := filepath.Join(filepath.Dir(dir), strings.ToUpper(filepath.Base(dir)))
	info, err := os.Stat(alias)
	original, origErr := os.Stat(dir)
	if err != nil || origErr != nil || !os.SameFile(info, original) {
		t.Skip("filesystem distinguishes case variants")
	}
	state := filepath.Join(dir, "states", "existing.state")
	mustWrite(t, state, `{"version":4}`)
	n.BackendConfig = map[string]string{"path": filepath.Join(alias, "states", "existing.state")}
	assertRelocationRefused(t, n, baseDir, dir)
	assertExists(t, state)
}

func TestAll_LegacySubdirWithSymlinkAliasedBackupRefusesUpgrade(t *testing.T) {
	n, baseDir, dir := legacySubdirFixture(t, "terraform {\n backend \"local\" {}\n}")
	alias := filepath.Join(baseDir, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "states", "existing.state.backup")
	mustWrite(t, state, `{"version":4}`)
	n.BackendConfig = map[string]string{"path": filepath.Join(alias, "states", "existing.state")}
	assertRelocationRefused(t, n, baseDir, dir)
	assertExists(t, state)
}
