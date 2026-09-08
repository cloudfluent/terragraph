package vendor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// setupThrowawayGitRepo creates a local git repo (git init, one commit including a main.tf and a README.md, tagged v1.0.0), everything the real git fetcher needs to be exercised end to end without any network access.
func setupThrowawayGitRepo(t *testing.T) (repoDir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	repoDir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=terragraph-test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=terragraph-test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-q", "-b", "main")
	mustWrite(t, filepath.Join(repoDir, "main.tf"), `output "id" { value = "x" }`)
	mustWrite(t, filepath.Join(repoDir, "README.md"), "# throwaway fixture module")
	run("add", ".")
	run("commit", "-q", "-m", "initial")
	run("tag", "v1.0.0")

	return repoDir
}

func TestGitFetcher_OfflineEndToEnd(t *testing.T) {
	repoDir := setupThrowawayGitRepo(t)

	baseDir := t.TempDir()
	src := "git::file://" + repoDir + "?ref=v1.0.0"
	nodes := []blueprint.Node{{Name: "vpc", Source: src}}
	manifestPath := filepath.Join(baseDir, "vendor.yaml")

	results, err := All(nodes, baseDir, "vendor", manifestPath, Options{})
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("expected a clean vendor, got %+v", results)
	}

	dst := filepath.Join(baseDir, "vendor", "vpc")
	assertExists(t, filepath.Join(dst, "main.tf"))
	assertExists(t, filepath.Join(dst, "README.md"))
	assertMissing(t, filepath.Join(dst, ".git"))

	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	entry := manifest["vpc"]
	if entry.Source != src {
		t.Fatalf("Source = %q, want %q", entry.Source, src)
	}

	// Hand-edit exclude into the manifest, as a user would, then force a re-vendor. README.md should now be pruned, and exclude must survive unchanged in the saved manifest.
	entry.Exclude = []string{"*.md"}
	manifest["vpc"] = entry
	if err := manifest.Save(manifestPath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := All(nodes, baseDir, "vendor", manifestPath, Options{Force: true}); err != nil {
		t.Fatalf("forced All: %v", err)
	}

	assertMissing(t, filepath.Join(dst, "README.md"))
	assertExists(t, filepath.Join(dst, "main.tf"))

	final, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest (final): %v", err)
	}
	if len(final["vpc"].Exclude) != 1 || final["vpc"].Exclude[0] != "*.md" {
		t.Fatalf("expected exclude to survive the re-vendor, got %v", final["vpc"].Exclude)
	}
}

func TestGitFetcher_SourceChangeRefetchesWithoutForce(t *testing.T) {
	repoDir := setupThrowawayGitRepo(t)

	// A second tag, v2.0.0, pointing at a different commit (README.md removed). Simulates a real ref bump in blueprint.hcl.
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=terragraph-test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=terragraph-test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.Remove(filepath.Join(repoDir, "README.md")); err != nil {
		t.Fatalf("removing README.md: %v", err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "v2")
	run("tag", "v2.0.0")

	baseDir := t.TempDir()
	manifestPath := filepath.Join(baseDir, "vendor.yaml")
	dst := filepath.Join(baseDir, "vendor", "vpc")

	v1 := blueprint.Node{Name: "vpc", Source: "git::file://" + repoDir + "?ref=v1.0.0"}
	if _, err := All([]blueprint.Node{v1}, baseDir, "vendor", manifestPath, Options{}); err != nil {
		t.Fatalf("vendoring v1: %v", err)
	}
	assertExists(t, filepath.Join(dst, "README.md"))

	v2 := blueprint.Node{Name: "vpc", Source: "git::file://" + repoDir + "?ref=v2.0.0"}
	results, err := All([]blueprint.Node{v2}, baseDir, "vendor", manifestPath, Options{})
	if err != nil {
		t.Fatalf("vendoring v2: %v", err)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("expected the ref bump to trigger a re-vendor (not skip), got %+v", results)
	}
	assertMissing(t, filepath.Join(dst, "README.md"))
}

func TestAll_FailedGitRefLeavesNoVendoredTree(t *testing.T) {
	repoDir := setupThrowawayGitRepo(t)
	baseDir := t.TempDir()
	nodes := []blueprint.Node{{Name: "vpc", Source: "git::file://" + repoDir + "?ref=missing-ref"}}
	manifestPath := filepath.Join(baseDir, "vendor.yaml")
	for attempt := 0; attempt < 2; attempt++ {
		results, err := All(nodes, baseDir, "vendor", manifestPath, Options{})
		if err != nil || len(results) != 1 || results[0].Err == nil || results[0].Skipped {
			t.Fatalf("attempt %d: results = %+v, err = %v, want failed fetch", attempt, results, err)
		}
		assertMissing(t, filepath.Join(baseDir, "vendor", "vpc"))
		assertMissing(t, manifestPath)
	}
}

func TestAll_FailedRefBumpPreservesTree(t *testing.T) {
	repoDir := setupThrowawayGitRepo(t)
	baseDir := t.TempDir()
	nodes := []blueprint.Node{{Name: "vpc", Source: "git::file://" + repoDir + "?ref=v1.0.0"}}
	manifestPath := filepath.Join(baseDir, "vendor.yaml")
	results, err := All(nodes, baseDir, "vendor", manifestPath, Options{})
	if err != nil || len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %+v, err = %v", results, err)
	}
	dst := filepath.Join(baseDir, "vendor", "vpc")
	mustWrite(t, filepath.Join(dst, "review-notes.md"), "local review")
	manifestBefore, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	nodes[0].Source = "git::file://" + repoDir + "?ref=missing-ref"
	results, err = All(nodes, baseDir, "vendor", manifestPath, Options{})
	if err != nil || len(results) != 1 || results[0].Err == nil {
		t.Fatalf("results = %+v, err = %v, want failed fetch", results, err)
	}
	assertExists(t, filepath.Join(dst, "review-notes.md"))
	assertExists(t, filepath.Join(dst, "main.tf"))
	manifestAfter, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(manifestBefore) != string(manifestAfter) {
		t.Fatalf("manifest changed after failed fetch")
	}
}

func TestAll_ExistingTreeWithoutManifestIsPreserved(t *testing.T) {
	baseDir := t.TempDir()
	dst := filepath.Join(baseDir, "vendor", "vpc")
	mustWrite(t, filepath.Join(dst, "main.tf"), `output "id" { value = "local" }`)
	results, err := All([]blueprint.Node{{Name: "vpc", Source: "git::file:///missing/repository"}}, baseDir, "vendor", filepath.Join(baseDir, "vendor.yaml"), Options{})
	if err != nil || len(results) != 1 || !results[0].Skipped || results[0].Err != nil {
		t.Fatalf("results = %+v, err = %v, want preserved legacy tree", results, err)
	}
	assertExists(t, filepath.Join(dst, "main.tf"))
}
