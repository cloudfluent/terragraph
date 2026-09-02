package graph

import (
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func TestBuild_GroupExpansion(t *testing.T) {
	root, bpPath := setupGroupFixture(t)

	bp, err := blueprint.ParseFile(bpPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, name := range []string{"vpc", "inst.a", "inst.b"} {
		if _, ok := g.Nodes[name]; !ok {
			t.Fatalf("expected node %q in expanded graph, got nodes: %v", name, nodeNames(g))
		}
	}

	wantADir, _ := filepath.Abs(filepath.Join(root, "modules/a"))
	if g.Nodes["inst.a"].Dir != wantADir {
		t.Fatalf("inst.a Dir = %q, want %q", g.Nodes["inst.a"].Dir, wantADir)
	}

	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected a valid graph, got problems: %v", problems)
	}

	// The outer edge (vpc -> use.inst.input.x) must have been rewritten to point at the real internal node "inst.a".
	foundOuter := false
	// The group's own internal edge (a.y -> b.z) must have been namespaced and preserved.
	foundInternal := false
	for _, e := range g.Edges {
		if e.From.Node == "vpc" && e.To.Node == "inst.a" && e.To.Name == "x" {
			foundOuter = true
		}
		if e.From.Node == "inst.a" && e.To.Node == "inst.b" && e.From.Name == "y" && e.To.Name == "z" {
			foundInternal = true
		}
	}
	if !foundOuter {
		t.Fatalf("expected the outer edge to be rewritten to vpc -> inst.a.input.x, got edges: %+v", g.Edges)
	}
	if !foundInternal {
		t.Fatalf("expected the group's internal edge to be preserved as inst.a -> inst.b, got edges: %+v", g.Edges)
	}

	order, err := TopoSort(g)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if indexIn(order, "vpc") > indexIn(order, "inst.a") {
		t.Fatalf("expected vpc before inst.a, got order %v", order)
	}
	if indexIn(order, "inst.a") > indexIn(order, "inst.b") {
		t.Fatalf("expected inst.a before inst.b, got order %v", order)
	}
}

func nodeNames(g *Graph) []string {
	names := make([]string, 0, len(g.Nodes))
	for n := range g.Nodes {
		names = append(names, n)
	}
	return names
}

func TestBuild_GroupExportFanOut(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `
output "vid" { value = "vpc-123" }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/b/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  node "b" { source = "../../modules/b" }

  export {
    input "x" { to = [node.a.input.x, node.b.input.x] }
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

	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	count := 0
	for _, e := range g.Edges {
		if e.From.Node == "vpc" {
			count++
			if e.To.Node != "inst.a" && e.To.Node != "inst.b" {
				t.Fatalf("unexpected fan-out target: %s", e.To.Node)
			}
		}
	}
	if count != 2 {
		t.Fatalf("expected the exposed input to fan out into 2 edges, got %d", count)
	}

	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("fan-out to distinct leaf inputs is not a collision, got problems: %v", problems)
	}
}

func TestBuild_ImplicitEdgeToGroupExpandsToRoots(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `
output "vid" { value = "x" }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/main.tf"), ``)
	writeFixtureFile(t, filepath.Join(root, "modules/b/main.tf"), ``)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  node "b" { source = "../../modules/b" }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./modules/vpc" }
use "g" {
    as     = "inst"
    source = "./groups/g"
  }
edge {
  from = node.vpc
  to   = use.inst
}
`)

	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := map[string]bool{}
	for _, e := range g.Edges {
		if e.From.Node == "vpc" {
			got[e.To.Node] = true
		}
	}
	if !got["inst.a"] || !got["inst.b"] {
		t.Fatalf("expected implicit edge to expand to both group roots (a and b, independent), got %+v", got)
	}
}

func TestBuild_EncapsulationRejectsUnexportedPort(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `output "vid" { value = "x" }`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `variable "x" { type = string }`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  export {}
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

	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if _, err := Build(bp, root); err == nil {
		t.Fatalf("expected an error: input.x is not exported by group g")
	}
}

func TestBuild_GroupSelfReferenceCycleRejected(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  use "g" {
    as     = "self"
    source = "."
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "g" {
    as     = "inst"
    source = "./groups/g"
  }
`)

	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if _, err := Build(bp, root); err == nil {
		t.Fatalf("expected an error for a group that transitively uses itself")
	}
}

func TestBuild_NestedGroup(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/leaf/outputs.tf"), `output "v" { value = "leaf" }`)
	writeFixtureFile(t, filepath.Join(root, "groups/inner/group.hcl"), `
group "inner" {
  node "leaf" { source = "../../modules/leaf" }
  export {
    output "v" { from = node.leaf.output.v }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "groups/outer/group.hcl"), `
group "outer" {
  use "inner" {
    as     = "inner"
    source = "../inner"
  }
  export {
    output "v" { from = use.inner.output.v }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "outer" {
    as     = "top"
    source = "./groups/outer"
  }
`)

	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, ok := g.Nodes["top.inner.leaf"]; !ok {
		t.Fatalf("expected nested group's leaf node to be namespaced as top.inner.leaf, got nodes: %v", nodeNames(g))
	}
}

func TestBuild_TwoOuterEdgesToSameExportInputIsError(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `
output "vid" { value = "vpc-123" }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/other/outputs.tf"), `
output "id" { value = "other-1" }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  export {
    input "x" { to = node.a.input.x }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc"   { source = "./modules/vpc" }
node "other" { source = "./modules/other" }
use "g" {
  as     = "inst"
  source = "./groups/g"
}
edge {
  from = node.vpc.output.vid
  to   = use.inst.input.x
}
edge {
  from = node.other.output.id
  to   = use.inst.input.x
}
`)

	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem after expansion, got %d: %v", len(problems), problems)
	}
	if !problems[0].IsError() {
		t.Fatalf("expected an Error, got %v", problems[0])
	}
	want := "node.inst.a.input.x: set by more than one data edge; remove extras"
	if problems[0].Message != want {
		t.Fatalf("message = %q, want %q", problems[0].Message, want)
	}
}

func TestBuild_OuterAndInternalEdgeConvergeOnSameInputIsError(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `
output "vid" { value = "vpc-123" }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/b/outputs.tf"), `
output "id" { value = "b-1" }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  node "b" { source = "../../modules/b" }

  edge {
    from = node.b.output.id
    to   = node.a.input.x
  }

  export {
    input "x" { to = node.a.input.x }
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

	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem after expansion, got %d: %v", len(problems), problems)
	}
	if !problems[0].IsError() {
		t.Fatalf("expected an Error, got %v", problems[0])
	}
	want := "node.inst.a.input.x: set by more than one data edge; remove extras"
	if problems[0].Message != want {
		t.Fatalf("message = %q, want %q", problems[0].Message, want)
	}
}

// An edge with nested input blocks is a shorthand for one data edge per block, so each expanded edge resolves through the instance's export exactly as a separately written one would, fan-out included.
func TestBuild_EdgeInputBlocksResolveThroughExport(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `
output "vid"     { value = "vpc-123" }
output "subnets" { value = ["a", "b"] }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
variable "s" { type = list(string) }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/b/variables.tf"), `
variable "s" { type = list(string) }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  node "b" { source = "../../modules/b" }

  export {
    input "x"       { to = node.a.input.x }
    input "subnets" { to = [node.a.input.s, node.b.input.s] }
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
  from = node.vpc
  to   = use.inst

  input "x"       { from = output.vid }
  input "subnets" { from = output.subnets }
}
`)

	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := map[string]string{}
	for _, e := range g.Edges {
		if e.From.Node == "vpc" {
			got[e.To.Node+"."+e.To.Name] = e.From.Name
		}
	}
	want := map[string]string{
		"inst.a.x": "vid",
		"inst.a.s": "subnets",
		"inst.b.s": "subnets",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d expanded edges out of vpc, got %d: %+v", len(want), len(got), got)
	}
	for target, output := range want {
		if got[target] != output {
			t.Fatalf("%s fed by output %q, want %q (all: %+v)", target, got[target], output, got)
		}
	}

	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected a valid graph, got problems: %v", problems)
	}
}

// The one-source-per-input rule applies to expanded edges like any other: writing the shorthand does not exempt an input from colliding with a separately declared edge.
func TestBuild_EdgeInputBlockCollidesWithSeparateEdge(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `output "vid" { value = "x" }`)
	writeFixtureFile(t, filepath.Join(root, "modules/other/outputs.tf"), `output "vid" { value = "y" }`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `variable "x" { type = string }`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc"   { source = "./modules/vpc" }
node "other" { source = "./modules/other" }
node "a"     { source = "./modules/a" }

edge {
  from = node.vpc
  to   = node.a

  input "x" { from = output.vid }
}

edge {
  from = node.other.output.vid
  to   = node.a.input.x
}
`)

	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	problems := Validate(g)
	if len(problems) != 1 || !problems[0].IsError() {
		t.Fatalf("expected 1 Error for the colliding input, got %v", problems)
	}
	want := "node.a.input.x: set by more than one data edge; remove extras"
	if problems[0].Message != want {
		t.Fatalf("message = %q, want %q", problems[0].Message, want)
	}
}
