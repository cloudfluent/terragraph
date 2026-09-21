//go:build !windows

package cli

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestGraphDetail_NeedsNoRuntimeAndWritesNothing proves inspection's read boundary (#94 §5): with
// no terraform/tofu reachable on PATH the detailed result still resolves, and nothing under
// .terragraph (or any engine-managed artifact) appears anywhere in the fixture tree afterward.
func TestGraphDetail_NeedsNoRuntimeAndWritesNothing(t *testing.T) {
	root := writeDetailFixture(t)
	t.Setenv("PATH", "/nonexistent-terragraph-detail-test")
	out, stderr, err := runDetailCmd(t, root)
	if err != nil || stderr != "" {
		t.Fatalf("got = %v, stderr %q, want a clean success with no runtime on PATH", err, stderr)
	}
	if !strings.Contains(out, "node checkout.cluster") || !strings.Contains(out, "node payments.core.nodegroup") {
		t.Fatalf("got = %q, want both group instances inspected", out)
	}
	jsonOut, _, err := runDetailCmd(t, root, "--output", "json")
	if err != nil || !strings.Contains(jsonOut, `"schema_version":1`) {
		t.Fatalf("got = %v, %q, want the JSON envelope without a runtime", err, jsonOut)
	}

	managed := []string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".terragraph" || strings.HasPrefix(d.Name(), ".terragraph.")) {
			managed = append(managed, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking fixture: %v", err)
	}
	if len(managed) != 0 {
		t.Fatalf("inspection created managed artifacts: %v", managed)
	}
}
