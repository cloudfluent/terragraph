//go:build !windows

package exec

import (
	"os"
	"path/filepath"
	"testing"
)

// Windows largely ignores the mode bits os.WriteFile is given, so permission assertions live here rather than in tfvars_test.go.

func TestWriteTFVars_OwnerOnlyPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".terragraph.vpc.tfvars.json")

	if _, err := WriteTFVars(path, map[string]any{"vpc_id": "vpc-123"}); err != nil {
		t.Fatalf("WriteTFVars: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("tfvars perm = %o, want 0600 (resolved inputs may be secret outputs; the file is never shared)", got)
	}
}

// os.WriteFile applies perm only on create, so the rewrite path removes first: a 0644 leftover from before this hygiene fix must end at 0600 after any successful write.
func TestWriteTFVars_RewriteTightensExistingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".terragraph.vpc.tfvars.json")

	if err := os.WriteFile(path, []byte(`{"stale": true}`), 0o644); err != nil {
		t.Fatalf("seeding world-readable leftover: %v", err)
	}
	if _, err := WriteTFVars(path, map[string]any{"vpc_id": "vpc-123"}); err != nil {
		t.Fatalf("WriteTFVars over leftover: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("tfvars perm after rewrite = %o, want 0600 (a pre-existing 0644 must not survive the write)", got)
	}
}
