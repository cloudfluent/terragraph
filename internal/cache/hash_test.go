package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestHashDir_DeterministicRegardlessOfCreationOrder(t *testing.T) {
	dirA := t.TempDir()
	writeFile(t, dirA, "main.tf", "resource {}")
	writeFile(t, dirA, "variables.tf", "variable {}")

	dirB := t.TempDir()
	writeFile(t, dirB, "variables.tf", "variable {}")
	writeFile(t, dirB, "main.tf", "resource {}")

	hashA, err := HashDir(dirA)
	if err != nil {
		t.Fatalf("HashDir(dirA): %v", err)
	}
	hashB, err := HashDir(dirB)
	if err != nil {
		t.Fatalf("HashDir(dirB): %v", err)
	}
	if hashA != hashB {
		t.Fatalf("expected identical hashes, got %s vs %s", hashA, hashB)
	}
}

func TestHashDir_ChangesWhenContentChanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", "resource {}")
	before, err := HashDir(dir)
	if err != nil {
		t.Fatalf("HashDir: %v", err)
	}

	writeFile(t, dir, "main.tf", "resource { changed = true }")
	after, err := HashDir(dir)
	if err != nil {
		t.Fatalf("HashDir: %v", err)
	}
	if before == after {
		t.Fatalf("expected hash to change after content change")
	}
}

func TestHashDir_OnlyTFFilesAndLockFileCount(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", "resource {}")
	before, err := HashDir(dir)
	if err != nil {
		t.Fatalf("HashDir: %v", err)
	}

	// None of these should affect the hash: state, the engine's own ephemeral tfvars file, downloaded provider binaries, and (the specific bug this test guards against) a file a *resource* writes into its own module directory (e.g. local_file), which otherwise wouldn't exist yet on the run that creates it but would on every run after, permanently defeating the cache.
	writeFile(t, dir, "terraform.tfstate", "{\"state\":1}")
	writeFile(t, dir, "terraform.tfstate.backup", "{\"state\":0}")
	writeFile(t, dir, ".terragraph.vpc.tfvars.json", `{"x":1}`)
	writeFile(t, dir, "vpc_id.txt", "vpc-123") // a resource's own output artifact
	if err := os.MkdirAll(filepath.Join(dir, ".terraform", "providers"), 0o755); err != nil {
		t.Fatalf("mkdir .terraform: %v", err)
	}
	writeFile(t, filepath.Join(dir, ".terraform"), "some-provider-binary", "binary-data")

	after, err := HashDir(dir)
	if err != nil {
		t.Fatalf("HashDir: %v", err)
	}
	if before != after {
		t.Fatalf("expected non-source files/dirs not to affect the hash: before=%s after=%s", before, after)
	}
}

func TestHashDir_LockFileChangeAffectsHash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", "resource {}")
	before, err := HashDir(dir)
	if err != nil {
		t.Fatalf("HashDir: %v", err)
	}

	writeFile(t, dir, ".terraform.lock.hcl", `provider "x" { version = "1.0.0" }`)
	after, err := HashDir(dir)
	if err != nil {
		t.Fatalf("HashDir: %v", err)
	}
	if before == after {
		t.Fatalf("expected a provider lock file change (version pin) to affect the hash")
	}
}

// TestHashDir_LockFileCreatedByFirstInitDoesNotPermanentlyBustCache guards against a real bug found via end-to-end testing: engine.Apply hashes a node's source both before running init/apply (to decide whether to skip) and after (to decide what to store for next time). Since .terraform.lock.hcl counts as source but is only created by the first-ever `terraform init`, storing the *pre*-init hash would never again match a future run's pre-init hash (which would now see the lock file), permanently defeating the cache after the very first apply. This test simulates that sequence directly against HashDir: hash before the lock file exists (what engine.Apply would compute pre-init on a first run), then hash after it's created (what it must store instead), then confirm that stored value matches a subsequent pre-init hash of the now-unchanged directory (what the next run's skip-check compares against).
func TestHashDir_LockFileCreatedByFirstInitDoesNotPermanentlyBustCache(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", "resource {}")

	preInitHash, err := HashDir(dir) // run 1, before init
	if err != nil {
		t.Fatalf("HashDir (pre-init): %v", err)
	}

	// Simulate `terraform init` creating the lock file on its first run.
	writeFile(t, dir, ".terraform.lock.hcl", `provider "x" { version = "1.0.0" }`)

	postInitHash, err := HashDir(dir) // what must be stored, not preInitHash
	if err != nil {
		t.Fatalf("HashDir (post-init): %v", err)
	}
	if postInitHash == preInitHash {
		t.Fatalf("expected the lock file's creation to change the hash (otherwise this test isn't exercising the bug)")
	}

	// Run 2: nothing changes, lock file already exists and is stable.
	run2PreInitHash, err := HashDir(dir)
	if err != nil {
		t.Fatalf("HashDir (run 2 pre-init): %v", err)
	}
	if run2PreInitHash != postInitHash {
		t.Fatalf("run 2's pre-init hash must match what run 1 stored (post-init), got %s vs stored %s", run2PreInitHash, postInitHash)
	}
}

func TestHashInputs_KeyOrderIndependent(t *testing.T) {
	a := map[string]any{"vpc_id": "vpc-1", "count": float64(3)}
	b := map[string]any{"count": float64(3), "vpc_id": "vpc-1"}

	hashA, err := HashInputs(a)
	if err != nil {
		t.Fatalf("HashInputs(a): %v", err)
	}
	hashB, err := HashInputs(b)
	if err != nil {
		t.Fatalf("HashInputs(b): %v", err)
	}
	if hashA != hashB {
		t.Fatalf("expected identical hashes regardless of map construction order")
	}
}

func TestHashInputs_ChangesWithValue(t *testing.T) {
	a := map[string]any{"vpc_id": "vpc-1"}
	b := map[string]any{"vpc_id": "vpc-2"}

	hashA, _ := HashInputs(a)
	hashB, _ := HashInputs(b)
	if hashA == hashB {
		t.Fatalf("expected different hashes for different values")
	}
}

func TestCombine_ChangesWithRuntimeIdentity(t *testing.T) {
	terraform := Combine("src", "in", "terraform\x00")
	tofu := Combine("src", "in", "tofu\x00")
	if terraform == tofu {
		t.Fatalf("expected switching runtime identity (e.g. terraform -> tofu) to change the combined hash")
	}
}

func TestCombine_SameInputsSameHash(t *testing.T) {
	a := Combine("src", "in", "terraform\x00>= 1.8.0")
	b := Combine("src", "in", "terraform\x00>= 1.8.0")
	if a != b {
		t.Fatalf("expected identical inputs to produce identical hashes")
	}
}
