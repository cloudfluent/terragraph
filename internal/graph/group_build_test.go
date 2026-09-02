package graph

import (
	"os"
	"path/filepath"
	"testing"
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
