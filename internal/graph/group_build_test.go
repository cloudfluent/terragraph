package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// setupGroupFixture builds, under a temp dir:
//
//	modules/vpc:         output "vid"
//	modules/a:           variable "x" (required), output "y"
//	modules/b:           variable "z" (required)
//	groups/g/group.hcl:  group "g": node a, node b, internal edge a.y->b.z,
//	                     export input "x" -> a.x, export output "y" -> a.y
//	blueprint.hcl:       node vpc; use "g" as "inst"; edge vpc.vid -> use.inst.input.x
//
// returning the root dir and the path to blueprint.hcl.
func setupGroupFixture(t *testing.T) (root, blueprintPath string) {
	t.Helper()
	root = t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `
output "vid" { value = "vpc-123" }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/outputs.tf"), `
output "y" { value = var.x }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/b/variables.tf"), `
variable "z" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  node "b" { source = "../../modules/b" }

  edge {
    from = node.a.output.y
    to   = node.b.input.z
  }

  export {
    input "x" { to = node.a.input.x }
    output "y" { from = node.a.output.y }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./modules/vpc" }

use "g" {
  as     = "inst"
  source = "./groups/g"
}

edge {
  from = node.vpc.output.vid
  to   = use.inst.input.x
}
`)

	return root, filepath.Join(root, "blueprint.hcl")
}

// setupSameGroupTwiceFixture builds, under a temp dir, a group instantiated twice from the same source directory:
//
//	modules/a:           variable "greeting" (default ""), output "echo"
//	groups/g/group.hcl:  group "g": node a { vars = { greeting = "hi" } }
//	blueprint.hcl:       use "g" as "first"; use "g" as "second" (same source)
//
// returning the root dir and the path to blueprint.hcl. Exercises resolveContext.parseGroupDir's cache: both `use` blocks resolve the same group source directory.
func setupSameGroupTwiceFixture(t *testing.T) (root, blueprintPath string) {
	t.Helper()
	root = t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
terraform {
  backend "local" {}
}
variable "greeting" {
  type    = string
  default = ""
}
`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/outputs.tf"), `
output "echo" { value = var.greeting }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" {
    source = "../../modules/a"
    vars   = { greeting = "hi" }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "g" {
  as     = "first"
  source = "./groups/g"
}

use "g" {
  as     = "second"
  source = "./groups/g"
}
`)

	return root, filepath.Join(root, "blueprint.hcl")
}

// TestBuild_SameGroupDirectoryUsedTwice_InstancesDontShareState proves the resolveContext.parseGroupDir cache (both `use` blocks below resolve the exact same group source directory) doesn't let two instances of the same group alias each other's Vars/BackendConfig maps: see cloneNode in build.go.
func TestBuild_SameGroupDirectoryUsedTwice_InstancesDontShareState(t *testing.T) {
	root, bpPath := setupSameGroupTwiceFixture(t)

	bp, err := blueprint.ParseFile(bpPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	first, ok := g.Nodes["first.a"]
	if !ok {
		t.Fatalf("expected node %q, got nodes: %v", "first.a", nodeNames(g))
	}
	second, ok := g.Nodes["second.a"]
	if !ok {
		t.Fatalf("expected node %q, got nodes: %v", "second.a", nodeNames(g))
	}

	if first.Vars["greeting"] != "hi" || second.Vars["greeting"] != "hi" {
		t.Fatalf("expected both instances to resolve vars.greeting = %q, got %+v / %+v", "hi", first.Vars, second.Vars)
	}

	first.Vars["greeting"] = "mutated"
	if second.Vars["greeting"] != "hi" {
		t.Fatalf("mutating the first instance's Vars affected the second instance's Vars: %+v", second.Vars)
	}

	if first.BackendConfig["path"] == "" || first.BackendConfig["path"] == second.BackendConfig["path"] {
		t.Fatalf("expected distinct filled backend paths, got %+v / %+v", first.BackendConfig, second.BackendConfig)
	}
	if first.BackendConfig != nil {
		first.BackendConfig["path"] = "mutated"
		if second.BackendConfig["path"] == "mutated" {
			t.Fatalf("mutating the first instance's BackendConfig affected the second: %+v", second.BackendConfig)
		}
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected a valid graph after fill, got problems: %v", problems)
	}
}
