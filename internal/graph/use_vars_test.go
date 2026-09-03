package graph

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func parseAndBuild(t *testing.T, root string) *Graph {
	t.Helper()
	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

func parseAndBuildErr(t *testing.T, root string) error {
	t.Helper()
	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	_, err = Build(bp, root)
	return err
}

func TestBuild_UseVarsRewritesOntoLeaf(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/b/main.tf"), ``)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  node "b" { source = "../../modules/b" }

  export {
    input "name" { to = node.a.input.x }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "g" {
  as     = "inst"
  source = "./groups/g"
  vars = {
    name = "checkout"
  }
}
`)

	g := parseAndBuild(t, root)

	if got := g.Nodes["inst.a"].Vars["x"]; got != "checkout" {
		t.Fatalf("inst.a.Vars[x] = %+v, want %q", got, "checkout")
	}
	if _, ok := g.Nodes["inst.b"].Vars["x"]; ok {
		t.Fatalf("sibling leaf inst.b must not receive use.vars: %+v", g.Nodes["inst.b"].Vars)
	}

	problems := Validate(g)
	if len(problems) != 0 {
		t.Fatalf("expected no problems when use.vars fills the required export input, got %v", problems)
	}
}

func TestBuild_UseVarsFanOut(t *testing.T) {
	root := t.TempDir()

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
use "g" {
  as     = "inst"
  source = "./groups/g"
  vars = {
    x = "shared"
  }
}
`)

	g := parseAndBuild(t, root)

	if g.Nodes["inst.a"].Vars["x"] != "shared" || g.Nodes["inst.b"].Vars["x"] != "shared" {
		t.Fatalf("expected fan-out onto both leaves, got %+v / %+v", g.Nodes["inst.a"].Vars, g.Nodes["inst.b"].Vars)
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("fan-out of use.vars to distinct leaves is not a collision, got %v", problems)
	}
}

func TestBuild_UseVarsNestedExportForward(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/leaf/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/inner/group.hcl"), `
group "inner" {
  node "leaf" { source = "../../modules/leaf" }
  export {
    input "x" { to = node.leaf.input.x }
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
    input "x" { to = use.inner.input.x }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "outer" {
  as     = "top"
  source = "./groups/outer"
  vars = {
    x = "v"
  }
}
`)

	g := parseAndBuild(t, root)

	if got := g.Nodes["top.inner.leaf"].Vars["x"]; got != "v" {
		t.Fatalf("expected outer use.vars to land on flattened leaf, got %+v", g.Nodes["top.inner.leaf"].Vars)
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected a valid graph, got %v", problems)
	}
}

func TestBuild_UseVarsTwoInstancesDoNotShareMaps(t *testing.T) {
	root := t.TempDir()

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
use "g" {
  as     = "first"
  source = "./groups/g"
  vars = {
    x = "one"
  }
}

use "g" {
  as     = "second"
  source = "./groups/g"
  vars = {
    x = "two"
  }
}
`)

	g := parseAndBuild(t, root)

	if g.Nodes["first.a"].Vars["x"] != "one" || g.Nodes["second.a"].Vars["x"] != "two" {
		t.Fatalf("expected distinct instance vars, got %+v / %+v", g.Nodes["first.a"].Vars, g.Nodes["second.a"].Vars)
	}
	g.Nodes["first.a"].Vars["x"] = "mutated"
	if g.Nodes["second.a"].Vars["x"] != "two" {
		t.Fatalf("mutating the first instance's Vars affected the second: %+v", g.Nodes["second.a"].Vars)
	}
}

func TestBuild_UseVarsUnknownExportName(t *testing.T) {
	root := t.TempDir()

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
use "g" {
  as     = "inst"
  source = "./groups/g"
  vars = {
    not_exported = "x"
  }
}
`)

	err := parseAndBuildErr(t, root)
	if err == nil {
		t.Fatal("expected an error for a use.vars key that is not an export input")
	}
	if !strings.Contains(err.Error(), "use.inst.vars.not_exported is not an export input of this group") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuild_UseVarsInternalVariableNameIsNotExported(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
variable "secret" { type = string }
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
use "g" {
  as     = "inst"
  source = "./groups/g"
  vars = {
    secret = "nope"
  }
}
`)

	err := parseAndBuildErr(t, root)
	if err == nil {
		t.Fatal("expected an error: an internal variable name that is not exported is not a use.vars key")
	}
	if !strings.Contains(err.Error(), "use.inst.vars.secret is not an export input of this group") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuild_UseVarsDottedInternalPathIsUnknownExport(t *testing.T) {
	root := t.TempDir()

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
use "g" {
  as     = "inst"
  source = "./groups/g"
  vars = {
    "a.x" = "nope"
  }
}
`)

	err := parseAndBuildErr(t, root)
	if err == nil {
		t.Fatal("expected an error: a dotted internal path is just an unknown export name")
	}
	if !strings.Contains(err.Error(), "use.inst.vars.a.x is not an export input of this group") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuild_UseVarsAndOuterDataEdgeIsError(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `
output "vid" { value = "vpc-123" }
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
node "vpc" { source = "./modules/vpc" }
use "g" {
  as     = "inst"
  source = "./groups/g"
  vars = {
    x = "literal"
  }
}
edge {
  from = node.vpc.output.vid
  to   = use.inst.input.x
}
`)

	g := parseAndBuild(t, root)
	problems := Validate(g)
	if len(problems) != 1 || !problems[0].IsError() {
		t.Fatalf("expected 1 Error for use.vars plus a data edge, got %v", problems)
	}
	if !strings.Contains(problems[0].Message, "set by both a data edge and vars") {
		t.Fatalf("unexpected message: %v", problems[0])
	}
}

func TestBuild_UseVarsAndInternalDataEdgeIsError(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/src/outputs.tf"), `
output "id" { value = "from-internal" }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "src" { source = "../../modules/src" }
  node "a"   { source = "../../modules/a" }

  edge {
    from = node.src.output.id
    to   = node.a.input.x
  }

  export {
    input "x" { to = node.a.input.x }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "g" {
  as     = "inst"
  source = "./groups/g"
  vars = {
    x = "literal"
  }
}
`)

	g := parseAndBuild(t, root)
	problems := Validate(g)
	if len(problems) != 1 || !problems[0].IsError() {
		t.Fatalf("expected 1 Error for use.vars plus an internal data edge, got %v", problems)
	}
	if !strings.Contains(problems[0].Message, "set by both a data edge and vars") {
		t.Fatalf("unexpected message: %v", problems[0])
	}
}

func TestBuild_UseVarsAndGroupBodyVarsIsError(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" {
    source = "../../modules/a"
    vars = {
      x = "from-group"
    }
  }
  export {
    input "x" { to = node.a.input.x }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "g" {
  as     = "inst"
  source = "./groups/g"
  vars = {
    x = "from-use"
  }
}
`)

	err := parseAndBuildErr(t, root)
	if err == nil {
		t.Fatal("expected an error for group-body node.vars plus use.vars on the same leaf")
	}
	if !strings.Contains(err.Error(), "set by more than one vars source") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuild_UseVarsNestedThenOuterOnSameLeafIsError(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/leaf/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/inner/group.hcl"), `
group "inner" {
  node "leaf" { source = "../../modules/leaf" }
  export {
    input "x" { to = node.leaf.input.x }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "groups/outer/group.hcl"), `
group "outer" {
  use "inner" {
    as     = "inner"
    source = "../inner"
    vars = {
      x = "inner"
    }
  }
  export {
    input "x" { to = use.inner.input.x }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "outer" {
  as     = "top"
  source = "./groups/outer"
  vars = {
    x = "outer"
  }
}
`)

	err := parseAndBuildErr(t, root)
	if err == nil {
		t.Fatal("expected an error for nested use.vars plus outer use.vars on the same leaf")
	}
	if !strings.Contains(err.Error(), "set by more than one vars source") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuild_UseVarsTwoExportInputsSameLeafIsError(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  export {
    input "a" { to = node.a.input.x }
    input "b" { to = node.a.input.x }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "g" {
  as     = "inst"
  source = "./groups/g"
  vars = {
    a = "1"
    b = "2"
  }
}
`)

	err := parseAndBuildErr(t, root)
	if err == nil {
		t.Fatal("expected an error when two use.vars keys rewrite onto the same leaf")
	}
	if !strings.Contains(err.Error(), "set by more than one vars source") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuild_UseVarsFanOutPlusEdgeOnOneLeafIsError(t *testing.T) {
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
    input "shared" { to = [node.a.input.x, node.b.input.x] }
    input "only_a" { to = node.a.input.x }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./modules/vpc" }
use "g" {
  as     = "inst"
  source = "./groups/g"
  vars = {
    shared = "from-vars"
  }
}
edge {
  from = node.vpc.output.vid
  to   = use.inst.input.only_a
}
`)

	g := parseAndBuild(t, root)
	problems := Validate(g)
	if len(problems) != 1 || !problems[0].IsError() {
		t.Fatalf("expected 1 Error on the leaf that received both sources, got %v", problems)
	}
	if !strings.Contains(problems[0].Message, "node.inst.a.input.x") || !strings.Contains(problems[0].Message, "set by both a data edge and vars") {
		t.Fatalf("unexpected message: %v", problems[0])
	}
	if g.Nodes["inst.b"].Vars["x"] != "from-vars" {
		t.Fatalf("the uncollided leaf should still hold the fan-out literal, got %+v", g.Nodes["inst.b"].Vars)
	}
}

func TestBuild_UseVarsAndOrderingOnlyEdgeIsNotCollision(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `
output "vid" { value = "x" }
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
node "vpc" { source = "./modules/vpc" }
use "g" {
  as     = "inst"
  source = "./groups/g"
  vars = {
    x = "literal"
  }
}
edge {
  from = node.vpc
  to   = use.inst
}
`)

	g := parseAndBuild(t, root)
	if g.Nodes["inst.a"].Vars["x"] != "literal" {
		t.Fatalf("expected use.vars to still apply, got %+v", g.Nodes["inst.a"].Vars)
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("an ordering-only edge carries no value and must not collide with use.vars, got %v", problems)
	}
}
