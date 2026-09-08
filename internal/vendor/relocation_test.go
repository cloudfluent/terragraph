package vendor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func legacySubdirFixture(t *testing.T, contents string) (blueprint.Node, string, string) {
	t.Helper()
	repoDir := setupThrowawayGitRepo(t)
	mustWrite(t, filepath.Join(repoDir, "modules", "app", "main.tf"), contents)
	commitGitFixture(t, repoDir)
	baseDir := t.TempDir()
	dir := filepath.Join(baseDir, "vendor", "app")
	mustWrite(t, filepath.Join(dir, "main.tf"), contents)
	n := blueprint.Node{Name: "app", Source: "git::file://" + repoDir + "//modules/app?ref=HEAD"}
	if err := (Manifest{"app": Entry{Source: n.Source}}).Save(filepath.Join(baseDir, "vendor.yaml")); err != nil {
		t.Fatal(err)
	}
	return n, baseDir, dir
}

func assertRelocationRefused(t *testing.T, n blueprint.Node, baseDir, dir string) {
	t.Helper()
	manifestPath := filepath.Join(baseDir, "vendor.yaml")
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	moduleBefore, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := All([]blueprint.Node{n}, baseDir, "vendor", manifestPath, Options{Force: true})
	if err != nil || len(results) != 1 || results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "local state") {
		t.Fatalf("results = %+v, err = %v, want local-state relocation refusal", results, err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("manifest changed after refusal: %v", err)
	}
	moduleAfter, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil || string(moduleAfter) != string(moduleBefore) {
		t.Fatalf("module changed after refusal: %v", err)
	}
	assertMissing(t, filepath.Join(dir, blueprint.VendoredSourceFilename))
}

func TestAll_LegacySubdirWithImplicitLocalStateRefusesUpgrade(t *testing.T) {
	n, baseDir, dir := legacySubdirFixture(t, `output "id" { value = "legacy" }`)
	state := filepath.Join(dir, "terraform.tfstate")
	mustWrite(t, state, `{"version":4,"terraform_version":"1.5.7","serial":1,"lineage":"legacy","outputs":{},"resources":[]}`)
	assertRelocationRefused(t, n, baseDir, dir)
	assertExists(t, state)
}

func TestAll_LegacySubdirWithRelativeBackendStateRefusesUpgrade(t *testing.T) {
	n, baseDir, dir := legacySubdirFixture(t, `terraform {
  backend "local" { path = "../shared.state" }
}
output "id" { value = "legacy" }`)
	state := filepath.Join(dir, "..", "shared.state")
	mustWrite(t, state, `{"version":4,"serial":1,"lineage":"legacy","outputs":{},"resources":[]}`)
	assertRelocationRefused(t, n, baseDir, dir)
	assertExists(t, state)
}

func TestAll_LegacySubdirWithRelativeBackendOverrideRefusesUpgrade(t *testing.T) {
	n, baseDir, dir := legacySubdirFixture(t, `terraform {
  backend "local" {}
}`)
	n.BackendConfig = map[string]string{"path": "runtime.state"}
	mustWrite(t, filepath.Join(dir, "runtime.state"), `{"version":4}`)
	assertRelocationRefused(t, n, baseDir, dir)
}

func TestAll_LegacySubdirWithAbsoluteExternalStateCanUpgrade(t *testing.T) {
	n, baseDir, _ := legacySubdirFixture(t, `terraform {
  backend "local" {}
}`)
	state := filepath.Join(t.TempDir(), "existing.state")
	mustWrite(t, state, `{"version":4}`)
	n.BackendConfig = map[string]string{"path": state}
	results, err := All([]blueprint.Node{n}, baseDir, "vendor", filepath.Join(baseDir, "vendor.yaml"), Options{})
	if err != nil || len(results) != 1 || results[0].Err != nil {
		t.Fatalf("results = %+v, err = %v, want upgrade preserving absolute state", results, err)
	}
	assertExists(t, state)
}

func TestAll_LegacySubdirWithAbsoluteInTreeStateRefusesUpgrade(t *testing.T) {
	n, baseDir, dir := legacySubdirFixture(t, `terraform {
  backend "local" {}
}`)
	state := filepath.Join(dir, "existing.state")
	mustWrite(t, state, `{"version":4}`)
	n.BackendConfig = map[string]string{"path": state}
	assertRelocationRefused(t, n, baseDir, dir)
	assertExists(t, state)
}

func TestAll_LegacySubdirWithUnknownBackendPathRefusesUpgrade(t *testing.T) {
	n, baseDir, dir := legacySubdirFixture(t, `terraform {
  backend "local" { path = var.state_path }
}`)
	results, err := All([]blueprint.Node{n}, baseDir, "vendor", filepath.Join(baseDir, "vendor.yaml"), Options{})
	if err != nil || len(results) != 1 || results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "cannot be determined") {
		t.Fatalf("results = %+v, err = %v, want unknown state path refusal", results, err)
	}
	assertMissing(t, filepath.Join(dir, blueprint.VendoredSourceFilename))
}

func TestAll_LegacySubdirWithLocalStateBackupRefusesUpgrade(t *testing.T) {
	n, baseDir, dir := legacySubdirFixture(t, `output "id" { value = "legacy" }`)
	state := filepath.Join(dir, "terraform.tfstate.backup")
	mustWrite(t, state, `{"version":4}`)
	assertRelocationRefused(t, n, baseDir, dir)
	assertExists(t, state)
}

func TestAll_LegacySubdirWithWorkspaceStateRefusesUpgrade(t *testing.T) {
	n, baseDir, dir := legacySubdirFixture(t, `output "id" { value = "legacy" }`)
	state := filepath.Join(dir, "terraform.tfstate.d", "prod", "terraform.tfstate")
	mustWrite(t, state, `{"version":4}`)
	assertRelocationRefused(t, n, baseDir, dir)
	assertExists(t, state)
}

func TestAll_LegacySubdirWithDivergentTofuStateRefusesUpgrade(t *testing.T) {
	n, baseDir, dir := legacySubdirFixture(t, `output "id" { value = "legacy" }`)
	mustWrite(t, filepath.Join(dir, "backend.tofu"), `terraform {
   backend "local" { path = "tofu-state/custom.json" }
 }`)
	state := filepath.Join(dir, "tofu-state", "custom.json.backup")
	mustWrite(t, state, `{"version":4}`)
	manifestPath := filepath.Join(baseDir, "vendor.yaml")
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	results, err := All([]blueprint.Node{n}, baseDir, "vendor", manifestPath, Options{})
	if err != nil || len(results) != 1 || results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "declarations differ") {
		t.Fatalf("results = %+v, err = %v, want ambiguous runtime relocation refusal", results, err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("manifest changed: %v", err)
	}
	stateAfter, err := os.ReadFile(state)
	if err != nil || string(stateAfter) != `{"version":4}` {
		t.Fatalf("state changed: %s, %v", stateAfter, err)
	}
	assertExists(t, filepath.Join(dir, "backend.tofu"))
	assertMissing(t, filepath.Join(dir, blueprint.VendoredSourceFilename))
}
