package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func groupVendorRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFixtureFile(t, filepath.Join(repo, "main.tf"), `output "id" { value = "remote" }`)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=terragraph-test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return "git::file://" + repo + "?ref=main"
}

func vendorFixtureCmd(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd("test")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"--blueprint", dir}, args...))
	err := root.Execute()
	if err != nil {
		t.Logf("%v: %v; stderr: %s", args, err, stderr.String())
	}
	return stdout.String(), err
}

func TestVendor_GroupRemoteNodesUseRootVendorDirectory(t *testing.T) {
	dir := t.TempDir()
	source := groupVendorRepo(t)
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), `vendor {
  directory = "third_party"
  manifest_file = "third_party.yaml"
}
use "service" {
  as = "prod"
  source = "./groups/service"
}`)
	writeFixtureFile(t, filepath.Join(dir, "groups", "service", "group.hcl"), fmt.Sprintf(`group "service" {
  node "vpc" { source = %q }
  export {
    output "id" { from = node.vpc.output.id }
  }
}`, source))
	out, err := vendorFixtureCmd(t, dir, "vendor")
	if err != nil || out != "prod.vpc: vendored\n" {
		t.Fatalf("stdout = %q, err = %v, want qualified vendoring", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "third_party", "prod.vpc", "main.tf")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "groups", "service", "vendor")); !os.IsNotExist(err) {
		t.Fatalf("vendor wrote inside the group source: %v", err)
	}
	if _, err := vendorFixtureCmd(t, dir, "graph"); err != nil {
		t.Fatalf("graph after group vendor: %v", err)
	}
}

func TestVendor_QualifiedNestedNodeSelectsOnlyOneInstance(t *testing.T) {
	dir := t.TempDir()
	source := groupVendorRepo(t)
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), `use "service" {
  as = "prod"
  source = "./groups/service"
}
use "service" {
  as = "dev"
  source = "./groups/service"
}`)
	writeFixtureFile(t, filepath.Join(dir, "groups", "service", "group.hcl"), `group "service" {
  use "component" {
    as = "inner"
    source = "../component"
  }
}`)
	writeFixtureFile(t, filepath.Join(dir, "groups", "component", "group.hcl"), fmt.Sprintf(`group "component" {
  node "vpc" { source = %q }
}`, source))
	out, err := vendorFixtureCmd(t, dir, "vendor", "--node", "prod.inner.vpc")
	if err != nil || out != "prod.inner.vpc: vendored\n" {
		t.Fatalf("stdout = %q, err = %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor", "dev.inner.vpc")); !os.IsNotExist(err) {
		t.Fatalf("unselected instance was vendored: %v", err)
	}
}

func TestVendor_LegacyGroupCopyIsPreservedWithoutForce(t *testing.T) {
	dir := t.TempDir()
	source := groupVendorRepo(t)
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), `use "service" {
  as = "prod"
  source = "./group"
}`)
	writeFixtureFile(t, filepath.Join(dir, "group", "group.hcl"), fmt.Sprintf(`vendor { directory = "existing" }
group "service" {
  node "vpc" { source = %q }
  export {
    output "id" { from = node.vpc.output.id }
  }
}`, source))
	writeFixtureFile(t, filepath.Join(dir, "group", "existing", "vpc", "main.tf"), `output "id" { value = "legacy" }`)
	out, err := vendorFixtureCmd(t, dir, "vendor")
	if err != nil || !strings.Contains(out, "prod.vpc: already vendored") {
		t.Fatalf("stdout = %q, err = %v, want legacy copy preserved", out, err)
	}
	if _, err := vendorFixtureCmd(t, dir, "graph"); err != nil {
		t.Fatalf("legacy graph: %v", err)
	}
}

func legacyGroupVendorFixture(t *testing.T, module string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	source := groupVendorRepo(t)
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), `use "service" {
   as = "prod"
   source = "./group"
 }`)
	writeFixtureFile(t, filepath.Join(dir, "group", "group.hcl"), fmt.Sprintf(`vendor { directory = "existing" }
 group "service" {
   node "vpc" { source = %q }
   export {
     output "id" { from = node.vpc.output.id }
   }
 }`, source))
	legacy := filepath.Join(dir, "group", "existing", "vpc")
	writeFixtureFile(t, filepath.Join(legacy, "main.tf"), module)
	return dir, legacy
}

func TestVendor_LegacyGroupStatePreventsWorkingDirectoryChange(t *testing.T) {
	dir, legacy := legacyGroupVendorFixture(t, `output "id" { value = "legacy" }`)
	state := filepath.Join(legacy, "terraform.tfstate")
	writeFixtureFile(t, state, `{"version":4}`)
	if _, err := vendorFixtureCmd(t, dir, "vendor", "--force"); err == nil {
		t.Fatal("vendor succeeded, want refusal while legacy local state exists")
	}
	after, err := os.ReadFile(state)
	if err != nil || string(after) != `{"version":4}` {
		t.Fatalf("state = %s, err = %v", after, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor", "prod.vpc")); !os.IsNotExist(err) {
		t.Fatalf("root copy was published despite state refusal: %v", err)
	}
	if _, err := vendorFixtureCmd(t, dir, "graph"); err != nil {
		t.Fatalf("legacy graph after refusal: %v", err)
	}
}

func TestVendor_LegacyGroupInheritedBackendStatePreventsRelocation(t *testing.T) {
	dir, legacy := legacyGroupVendorFixture(t, `terraform {
   backend "local" {}
 }
 output "id" { value = "legacy" }`)
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), `use "service" {
   as = "prod"
   source = "./group"
   backend_config = { path = "custom.state" }
 }`)
	writeFixtureFile(t, filepath.Join(legacy, "custom.state.backup"), `{"version":4}`)
	if _, err := vendorFixtureCmd(t, dir, "vendor", "--force"); err == nil {
		t.Fatal("vendor succeeded with inherited relative state")
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor", "prod.vpc")); !os.IsNotExist(err) {
		t.Fatalf("root copy published: %v", err)
	}
}

func TestVendor_LegacyGroupRefreshKeepsSourceCopy(t *testing.T) {
	dir, legacy := legacyGroupVendorFixture(t, `output "id" { value = "legacy" }`)
	if _, err := vendorFixtureCmd(t, dir, "vendor", "--force"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(legacy, "main.tf"))
	if err != nil || string(after) != `output "id" { value = "legacy" }` {
		t.Fatalf("legacy source was changed: %s, %v", after, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor", "prod.vpc", "main.tf")); err != nil {
		t.Fatal(err)
	}
	if _, err := vendorFixtureCmd(t, dir, "graph"); err != nil {
		t.Fatal(err)
	}
}

func TestGraph_BrokenRootGroupCopyNeverFallsBack(t *testing.T) {
	dir, _ := legacyGroupVendorFixture(t, `output "id" { value = "legacy" }`)
	writeFixtureFile(t, filepath.Join(dir, "vendor", "prod.vpc", ".terragraph-source.json"), `{"subdir":"missing"}`)
	if _, err := vendorFixtureCmd(t, dir, "graph"); err == nil {
		t.Fatal("graph used legacy copy despite broken root copy")
	}
}

func TestVendor_GroupCycleFailsBeforeFetching(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), `use "loop" {
   as = "prod"
   source = "./group"
 }`)
	writeFixtureFile(t, filepath.Join(dir, "group", "group.hcl"), fmt.Sprintf(`group "loop" {
   node "remote" { source = %q }
   use "loop" {
     as = "again"
     source = "./"
   }
 }`, groupVendorRepo(t)))
	if _, err := vendorFixtureCmd(t, dir, "vendor"); err == nil || !strings.Contains(err.Error(), "circular group use") {
		t.Fatalf("err = %v, want group cycle", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor")); !os.IsNotExist(err) {
		t.Fatalf("vendor wrote before group discovery completed: %v", err)
	}
}

func TestVendor_LegacyDefaultGroupPathRetainsPriorityOverCustom(t *testing.T) {
	dir, custom := legacyGroupVendorFixture(t, `output "id" { value = "custom" }`)
	previous := filepath.Join(dir, "group", "vendor", "vpc")
	writeFixtureFile(t, filepath.Join(previous, "main.tf"), `output "id" { value = "previous" }`)
	writeFixtureFile(t, filepath.Join(previous, "terraform.tfstate"), `{"version":4}`)
	// A broken custom marker makes a fallback regression observable even without running Terraform.
	writeFixtureFile(t, filepath.Join(custom, ".terragraph-source.json"), `{"subdir":"missing"}`)
	if _, err := vendorFixtureCmd(t, dir, "graph"); err != nil {
		t.Fatalf("graph stopped using its previous directory: %v", err)
	}
	if out, err := vendorFixtureCmd(t, dir, "vendor"); err != nil || !strings.Contains(out, "already vendored") {
		t.Fatalf("stdout = %q, err = %v", out, err)
	}
	if _, err := vendorFixtureCmd(t, dir, "vendor", "--force"); err == nil {
		t.Fatal("vendor bypassed local state in previous default path")
	}
	state, err := os.ReadFile(filepath.Join(previous, "terraform.tfstate"))
	if err != nil || string(state) != `{"version":4}` {
		t.Fatalf("previous state = %s, err = %v", state, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor", "prod.vpc")); !os.IsNotExist(err) {
		t.Fatalf("root copy published: %v", err)
	}
}
