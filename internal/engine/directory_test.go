package engine

import (
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
)

func TestLoad_DirectoryBlueprintPreservesNestedGroupDAG(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "environments", "prod")
	nodes := `node "seed" { source = "../../modules/seed" }
node "final" { source = "../../modules/join" }
`
	uses := `use "pair" {
 as = "west"
 source = "../../catalog"
}
use "pair" {
 as = "east"
 source = "../../catalog"
}
`
	edges := `edge {
 from = node.seed.output.id
 to = use.west.input.id
}
edge {
 from = node.seed.output.id
 to = use.east.input.id
}
edge {
 from = use.west.output.id
 to = node.final.input.left
}
edge {
 from = use.east.output.id
 to = node.final.input.right
}
`
	for path, content := range map[string]string{
		filepath.Join(dir, "nodes.hcl"):                       nodes,
		filepath.Join(dir, "uses.hcl"):                        uses,
		filepath.Join(dir, "edges.hcl"):                       edges,
		filepath.Join(dir, "comparison.txt"):                  nodes + uses + edges,
		filepath.Join(dir, ".terraform.lock.hcl"):             `provider "example" {}`,
		filepath.Join(root, "catalog", ".terraform.lock.hcl"): `provider "example" {}`,
		filepath.Join(root, "catalog", "leaf.hcl"): `group "leaf" {
 node "worker" { source = "../modules/worker" }
 export {
  input "id" { to = node.worker.input.id }
  output "id" { from = node.worker.output.id }
 }
}`,
		filepath.Join(root, "catalog", "nested", "wrapper.hcl"): `group "wrapper" {
 use "leaf" {
  as = "inner"
  source = ".."
 }
 export {
  input "id" { to = use.inner.input.id }
  output "id" { from = use.inner.output.id }
 }
}`,
		filepath.Join(root, "catalog", "pair.hcl"): `group "pair" {
 use "wrapper" {
  as = "left"
  source = "./nested"
 }
 use "wrapper" {
  as = "right"
  source = "./nested"
 }
 node "merge" { source = "../modules/join" }
 edge {
  from = use.left.output.id
  to = node.merge.input.left
 }
 edge {
  from = use.right.output.id
  to = node.merge.input.right
 }
 export {
  input "id" { to = [use.left.input.id, use.right.input.id] }
  output "id" { from = node.merge.output.id }
 }
}`,
		filepath.Join(root, "modules", "seed", "main.tf"): `output "id" { value = "seed" }`,
		filepath.Join(root, "modules", "worker", "main.tf"): `terraform {
 backend "local" {}
}
variable "id" { type = string }
output "id" { value = var.id }`,
		filepath.Join(root, "modules", "join", "main.tf"): `terraform {
 backend "local" {}
}
variable "left" { type = string }
variable "right" { type = string }
output "id" { value = var.left }`,
	} {
		if err := osWriteFile(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	e, err := Load(dir, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if problems := e.Validate(); len(problems) != 0 {
		t.Fatalf("got = %v, want no validation problems", problems)
	}
	single, err := Load(filepath.Join(dir, "comparison.txt"), exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.Graph, single.Graph) {
		t.Fatal("directory and single-file graphs differ")
	}
	wantLevels := [][]string{
		{"seed"},
		{"east.left.inner.worker", "east.right.inner.worker", "west.left.inner.worker", "west.right.inner.worker"},
		{"east.merge", "west.merge"},
		{"final"},
	}
	if levels, err := e.Levels(); err != nil || !reflect.DeepEqual(levels, wantLevels) {
		t.Fatalf("got = %v, %v, want %v", levels, err, wantLevels)
	}
	for name, node := range e.Graph.Nodes {
		module := "join"
		if name == "seed" {
			module = "seed"
		} else if strings.HasSuffix(name, ".worker") {
			module = "worker"
		}
		if want := filepath.Join(root, "modules", module); node.Dir != want {
			t.Fatalf("%s directory = %q, want %q", name, node.Dir, want)
		}
		if name != "seed" {
			if want := filepath.Join(dir, ".terragraph", "state", name+".tfstate"); node.BackendConfig["path"] != want {
				t.Fatalf("%s state = %q, want %q", name, node.BackendConfig["path"], want)
			}
		}
		if want := filepath.Join(dir, ".terragraph", "tfdata", name); e.dataDir(name) != want || single.dataDir(name) != want {
			t.Fatalf("%s data directory = %q, want %q", name, e.dataDir(name), want)
		}
	}
	sources, err := graph.SourceNodes(e.Blueprint, e.BaseDir)
	if err != nil || len(sources) != len(e.Graph.Nodes) {
		t.Fatalf("got = %v, %v, want the same vendor leaf set", sources, err)
	}
	for _, source := range sources {
		if e.Graph.Nodes[source.Name] == nil {
			t.Fatalf("vendor discovered unexpected leaf %q", source.Name)
		}
	}
	vars, err := e.resolveInputs("final", map[string]exec.Outputs{
		"west.merge": {"id": {Value: "west"}},
		"east.merge": {"id": {Value: "east"}},
	})
	if err != nil || vars["left"] != "west" || vars["right"] != "east" {
		t.Fatalf("got = %v, %v, want independent group outputs", vars, err)
	}
}
