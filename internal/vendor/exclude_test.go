package vendor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchExclude_BasenamePatternMatchesAnyDepth(t *testing.T) {
	if !matchExclude([]string{"*.md"}, "README.md") {
		t.Fatalf("expected top-level match")
	}
	if !matchExclude([]string{"*.md"}, "docs/nested/README.md") {
		t.Fatalf("expected nested match for a basename-only pattern")
	}
	if matchExclude([]string{"*.md"}, "main.tf") {
		t.Fatalf("expected no match")
	}
}

func TestMatchExclude_SlashPatternAnchorsToFullPath(t *testing.T) {
	if !matchExclude([]string{"docs/readme.md"}, "docs/readme.md") {
		t.Fatalf("expected exact anchored match")
	}
	if matchExclude([]string{"docs/readme.md"}, "other/docs/readme.md") {
		t.Fatalf("anchored pattern should not match at other depths")
	}
}

func TestMatchExclude_GitAlwaysExcludedRegardlessOfConfiguredPatterns(t *testing.T) {
	if !matchExclude(alwaysExcluded, ".git") {
		t.Fatalf("expected .git to match the always-excluded set")
	}
}

func TestPrune_RemovesMatchesAndAlwaysStripsGit(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.tf"), "x")
	mustWrite(t, filepath.Join(dir, "README.md"), "x")
	mustWrite(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main")

	if err := prune(dir, []string{"*.md"}); err != nil {
		t.Fatalf("prune: %v", err)
	}

	assertMissing(t, filepath.Join(dir, "README.md"))
	assertMissing(t, filepath.Join(dir, ".git"))
	assertExists(t, filepath.Join(dir, "main.tf"))
}

func TestPrune_NoPatternsStillStripsGit(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.tf"), "x")
	mustWrite(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main")

	if err := prune(dir, nil); err != nil {
		t.Fatalf("prune: %v", err)
	}

	assertMissing(t, filepath.Join(dir, ".git"))
	assertExists(t, filepath.Join(dir, "main.tf"))
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat err = %v", path, err)
	}
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}
