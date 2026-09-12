package graph

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func assertLoc(t *testing.T, label string, got, want blueprint.Loc) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got = %+v, want %+v", label, got, want)
	}
}

func assertPath(t *testing.T, label string, got []MappingStep, want []MappingStep) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d steps (%+v), want %d (%+v)", label, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s step %d: got %+v, want %+v", label, i, got[i], want[i])
		}
	}
}

// assertVarSource compares the comparable fields directly and the slice via assertPath, since Go cannot == a struct holding a slice.
func assertVarSource(t *testing.T, label string, got, want VarSource) {
	t.Helper()
	if got.From != want.From || got.Subject != want.Subject || got.Decl != want.Decl {
		t.Fatalf("%s: got %+v, want %+v", label, got, want)
	}
	assertPath(t, label+" mapping", got.MappingPath, want.MappingPath)
}

// setupEdgeProvenanceFixture builds, under a temp dir:
//
//	modules/vpc:         output "vid"
//	modules/a:           variable "x" (required), variable "w" (default), output "y"
//	modules/b:           variable "z" (required)
//	modules/c:           variable "z" (required)
//	groups/g/group.hcl:  group "g" (line 1): nodes a (line 2) and b (line 3),
//	                     internal edge a.y -> b.z (line 5), export input "x"
//	                     (block line 11, to line 12), export input "w" (block
//	                     line 14, to line 15), export output "y" (block line 17,
//	                     from line 18)
//	blueprint.hcl:       node vpc (line 1), node sink (line 3), use "g" as
//	                     "inst" (line 5), data edge vpc.vid -> use.inst.input.x
//	                     (line 10), second data edge vpc.vid -> use.inst.input.w
//	                     (line 15) — two distinct mappings between one pair —
//	                     data edge use.inst.output.y -> sink.z (line 20), and an
//	                     ordering-only edge vpc -> use.inst (line 25)
//
// returning the root dir. Every declaration line number the provenance tests
// assert against is pinned by this layout; edit the fixtures and the assertions
// together.
func setupEdgeProvenanceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `
output "vid" { value = "vpc-123" }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
variable "w" {
  type    = string
  default = "w"
}
`)
	writeFixtureFile(t, filepath.Join(root, "modules/a/outputs.tf"), `
output "y" { value = "x" }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/b/variables.tf"), `
variable "z" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/c/variables.tf"), `
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
    input "x" {
      to = node.a.input.x
    }
    input "w" {
      to = node.a.input.w
    }
    output "y" {
      from = node.a.output.y
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./modules/vpc" }

node "sink" { source = "./modules/c" }

use "g" {
  as     = "inst"
  source = "./groups/g"
}

edge {
  from = node.vpc.output.vid
  to   = use.inst.input.x
}

edge {
  from = node.vpc.output.vid
  to   = use.inst.input.w
}

edge {
  from = use.inst.output.y
  to   = node.sink.input.z
}

edge {
  from = node.vpc
  to   = use.inst
}
`)

	return root
}

// TestBuild_RewrittenEdgeKeepsDeclarationLoc proves rewriteEdge carries the
// original edge block's Loc onto every rewritten copy: inspection must point
// at the declaration to edit, and a fan-out must not lose it on some copies.
func TestBuild_RewrittenEdgeKeepsDeclarationLoc(t *testing.T) {
	root := setupEdgeProvenanceFixture(t)
	g := parseAndBuild(t, root)

	e := findEdge(t, g, "vpc", "vid", "inst.a", "x")
	assertLoc(t, "rewritten edge declaration", e.Loc, blueprint.Loc{File: filepath.Join(root, "blueprint.hcl"), Line: 11, Column: 1})
}

// TestBuild_EdgeMappingPathRecordsExportHops proves each rewritten edge carries
// the export hops its rewrite traversed — pinned at the mapping attribute the
// edge endpoint actually resolved through — that plain and ordering edges carry
// none, and that two distinct data mappings between one pair stay distinct.
func TestBuild_EdgeMappingPathRecordsExportHops(t *testing.T) {
	root := setupEdgeProvenanceFixture(t)
	g := parseAndBuild(t, root)
	groupFile := filepath.Join(root, "groups/g/group.hcl")

	x := findEdge(t, g, "vpc", "vid", "inst.a", "x")
	assertPath(t, "to-side hop", x.MappingPath, []MappingStep{
		{Via: "use.inst.input.x", Decl: blueprint.Loc{File: groupFile, Line: 13, Column: 7}},
	})

	w := findEdge(t, g, "vpc", "vid", "inst.a", "w")
	assertPath(t, "second mapping between the same pair", w.MappingPath, []MappingStep{
		{Via: "use.inst.input.w", Decl: blueprint.Loc{File: groupFile, Line: 16, Column: 7}},
	})

	y := findEdge(t, g, "inst.a", "y", "sink", "z")
	assertPath(t, "from-side hop", y.MappingPath, []MappingStep{
		{Via: "use.inst.output.y", Decl: blueprint.Loc{File: groupFile, Line: 19, Column: 7}},
	})

	// The ordering-only edge expands to the instance's internal roots on its to-side (a downstream's entry points must wait) with no ports and no mapping: no export was traversed.
	order := findEdge(t, g, "vpc", "", "inst.a", "")
	if order.IsDataEdge() || order.From.IsPort() || order.To.IsPort() {
		t.Fatalf("ordering edge grew ports: %+v", order)
	}
	assertPath(t, "ordering edge mapping", order.MappingPath, nil)

	// A group-body edge spliced upward keeps its own group-file declaration and has no mapping of its own.
	internal := findEdge(t, g, "inst.a", "y", "inst.b", "z")
	assertLoc(t, "group-internal edge declaration", internal.Loc, blueprint.Loc{File: groupFile, Line: 6, Column: 3})
	assertPath(t, "group-internal edge mapping", internal.MappingPath, nil)
}

// TestBuild_ProvAndVarSourcesSameGroupTwice proves two instances of one group
// each report their own instantiation path and their own use.vars declaration
// site, while the original node declaration still points into the group body
// both instances were expanded from.
func TestBuild_ProvAndVarSourcesSameGroupTwice(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
`)
	// groups/g/group.hcl: group block line 1, node "a" line 2, export input "x" block line 5.
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }

  export {
    input "x" {
      to = node.a.input.x
    }
  }
}
`)
	// blueprint.hcl: first use block line 1 (vars key x line 5), second use block line 9 (vars key x line 13).
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
	groupFile := filepath.Join(root, "groups/g/group.hcl")
	bpFile := filepath.Join(root, "blueprint.hcl")

	first, second := g.Nodes["first.a"], g.Nodes["second.a"]

	// The original node block both copies were expanded from, not either instantiation site.
	assertLoc(t, "first instance original declaration", first.Prov.Decl, blueprint.Loc{File: groupFile, Line: 3, Column: 3})
	assertLoc(t, "second instance original declaration", second.Prov.Decl, blueprint.Loc{File: groupFile, Line: 3, Column: 3})

	if len(first.Prov.Path) != 1 || len(second.Prov.Path) != 1 {
		t.Fatalf("expected one instance step each, got %+v / %+v", first.Prov.Path, second.Prov.Path)
	}
	if got, want := first.Prov.Path[0], (InstanceStep{Instance: "first", Group: "g", Use: blueprint.Loc{File: bpFile, Line: 2, Column: 1}, GroupDecl: blueprint.Loc{File: groupFile, Line: 2, Column: 1}}); got != want {
		t.Fatalf("first instance path step: got %+v, want %+v", got, want)
	}
	if got, want := second.Prov.Path[0], (InstanceStep{Instance: "second", Group: "g", Use: blueprint.Loc{File: bpFile, Line: 10, Column: 1}, GroupDecl: blueprint.Loc{File: groupFile, Line: 2, Column: 1}}); got != want {
		t.Fatalf("second instance path step: got %+v, want %+v", got, want)
	}

	// Each instance's supply points at its own use.vars declaration line, and through the group's export.
	assertVarSource(t, "first instance VarSources[x]", first.VarSources["x"], VarSource{
		From:    "use_vars",
		Subject: "use.first.vars.x",
		Decl:    blueprint.Loc{File: bpFile, Line: 6, Column: 5},
		MappingPath: []MappingStep{
			{Via: "use.first.input.x", Decl: blueprint.Loc{File: groupFile, Line: 6, Column: 5}},
		},
	})
	assertVarSource(t, "second instance VarSources[x]", second.VarSources["x"], VarSource{
		From:    "use_vars",
		Subject: "use.second.vars.x",
		Decl:    blueprint.Loc{File: bpFile, Line: 14, Column: 5},
		MappingPath: []MappingStep{
			{Via: "use.second.input.x", Decl: blueprint.Loc{File: groupFile, Line: 6, Column: 5}},
		},
	})
}

// TestBuild_NodeVarsSourcePointsAtOwnDeclaration proves a node block's own vars
// key reports kind node_vars pinned at that key's declaration, with no mapping
// (nothing propagated it), and that a root node has no instance path.
func TestBuild_NodeVarsSourcePointsAtOwnDeclaration(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/own/variables.tf"), `
variable "v" { type = string }
variable "n" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "own" {
  source = "./modules/own"
  vars = {
    v = "literal"
    n = null
  }
}
`)

	g := parseAndBuild(t, root)
	node := g.Nodes["own"]
	bpFile := filepath.Join(root, "blueprint.hcl")

	assertLoc(t, "root node declaration", node.Prov.Decl, blueprint.Loc{File: bpFile, Line: 2, Column: 1})
	if node.Prov.Path != nil {
		t.Fatalf("root node must have no instance path, got %+v", node.Prov.Path)
	}

	assertVarSource(t, "VarSources[v]", node.VarSources["v"], VarSource{From: "node_vars", Subject: "node.own.vars.v", Decl: blueprint.Loc{File: bpFile, Line: 5, Column: 5}})
	// An explicit null is still an explicit supply: never reclassified as absent.
	assertVarSource(t, "VarSources[n]", node.VarSources["n"], VarSource{From: "node_vars", Subject: "node.own.vars.n", Decl: blueprint.Loc{File: bpFile, Line: 6, Column: 5}})
}

// TestBuild_UseVarsFanOutMappingPathPerLeaf proves a fan-out export input is
// explainable at each destination leaf: every destination carries its own
// VarSource entry with the same mapping step.
func TestBuild_UseVarsFanOutMappingPathPerLeaf(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/a/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/b/variables.tf"), `
variable "x" { type = string }
`)
	// groups/g/group.hcl: export input "x" block line 5, fan-out to on line 6.
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  node "b" { source = "../../modules/b" }

  export {
    input "x" {
      to = [node.a.input.x, node.b.input.x]
    }
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
	groupFile := filepath.Join(root, "groups/g/group.hcl")

	for _, leaf := range []string{"inst.a", "inst.b"} {
		vs, ok := g.Nodes[leaf].VarSources["x"]
		if !ok {
			t.Fatalf("%s: fan-out destination has no VarSources[x]: %+v", leaf, g.Nodes[leaf].VarSources)
		}
		if vs.From != "use_vars" || vs.Subject != "use.inst.vars.x" {
			t.Fatalf("%s: got %+v", leaf, vs)
		}
		assertPath(t, leaf+" mapping", vs.MappingPath, []MappingStep{
			{Via: "use.inst.input.x", Decl: blueprint.Loc{File: groupFile, Line: 7, Column: 5}},
		})
	}
}

// TestBuild_UseVarsNestedTwoHopMappingPathOrder proves a supply crossing two
// export levels records its hops in original-declaration-to-leaf order, each
// hop pinned at the export input block that declared it in its own group file.
func TestBuild_UseVarsNestedTwoHopMappingPathOrder(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/leaf/variables.tf"), `
variable "x" { type = string }
`)
	// groups/inner/group.hcl: node "leaf" line 2, export input "x" block line 5, to line 6.
	writeFixtureFile(t, filepath.Join(root, "groups/inner/group.hcl"), `
group "inner" {
  node "leaf" { source = "../../modules/leaf" }

  export {
    input "x" {
      to = node.leaf.input.x
    }
  }
}
`)
	// groups/outer/group.hcl: use "inner" line 2, export input "x" block line 8, to line 9.
	writeFixtureFile(t, filepath.Join(root, "groups/outer/group.hcl"), `
group "outer" {
  use "inner" {
    as     = "inner"
    source = "../inner"
  }

  export {
    input "x" {
      to = use.inner.input.x
    }
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
	leaf := g.Nodes["top.inner.leaf"]
	innerFile := filepath.Join(root, "groups/inner/group.hcl")
	outerFile := filepath.Join(root, "groups/outer/group.hcl")
	bpFile := filepath.Join(root, "blueprint.hcl")

	assertVarSource(t, "two-hop VarSources[x]", leaf.VarSources["x"], VarSource{
		From:    "use_vars",
		Subject: "use.top.vars.x",
		Decl:    blueprint.Loc{File: bpFile, Line: 6, Column: 5},
		MappingPath: []MappingStep{
			{Via: "use.top.input.x", Decl: blueprint.Loc{File: outerFile, Line: 9, Column: 5}},
			{Via: "use.inner.input.x", Decl: blueprint.Loc{File: innerFile, Line: 6, Column: 5}},
		},
	})

	// The instance path is outermost-first with each step's use and group declarations.
	wantPath := []InstanceStep{
		{Instance: "top", Group: "outer", Use: blueprint.Loc{File: bpFile, Line: 2, Column: 1}, GroupDecl: blueprint.Loc{File: outerFile, Line: 2, Column: 1}},
		{Instance: "top.inner", Group: "inner", Use: blueprint.Loc{File: outerFile, Line: 3, Column: 3}, GroupDecl: blueprint.Loc{File: innerFile, Line: 2, Column: 1}},
	}
	if len(leaf.Prov.Path) != len(wantPath) {
		t.Fatalf("instance path: got %+v, want %+v", leaf.Prov.Path, wantPath)
	}
	for i, want := range wantPath {
		if leaf.Prov.Path[i] != want {
			t.Fatalf("instance path step %d: got %+v, want %+v", i, leaf.Prov.Path[i], want)
		}
	}
	assertLoc(t, "nested leaf original declaration", leaf.Prov.Decl, blueprint.Loc{File: innerFile, Line: 3, Column: 3})
}

// TestBuild_EdgeMappingPathNestedTwoHops proves a root edge targeting a nested
// group's export input records both export hops, pinned at each level's `to`
// attribute.
func TestBuild_EdgeMappingPathNestedTwoHops(t *testing.T) {
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/leaf/variables.tf"), `
variable "x" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/p/outputs.tf"), `
output "p" { value = "1" }
`)
	// Same nesting shape as TestBuild_UseVarsNestedTwoHopMappingPathOrder.
	writeFixtureFile(t, filepath.Join(root, "groups/inner/group.hcl"), `
group "inner" {
  node "leaf" { source = "../../modules/leaf" }

  export {
    input "x" {
      to = node.leaf.input.x
    }
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
    input "x" {
      to = use.inner.input.x
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "p" { source = "./modules/p" }

use "outer" {
  as     = "top"
  source = "./groups/outer"
}

edge {
  from = node.p.output.p
  to   = use.top.input.x
}
`)

	g := parseAndBuild(t, root)
	innerFile := filepath.Join(root, "groups/inner/group.hcl")
	outerFile := filepath.Join(root, "groups/outer/group.hcl")

	e := findEdge(t, g, "p", "p", "top.inner.leaf", "x")
	assertLoc(t, "nested rewritten edge declaration", e.Loc, blueprint.Loc{File: filepath.Join(root, "blueprint.hcl"), Line: 9, Column: 1})
	assertPath(t, "two-hop edge mapping", e.MappingPath, []MappingStep{
		{Via: "use.top.input.x", Decl: blueprint.Loc{File: outerFile, Line: 10, Column: 7}},
		{Via: "use.inner.input.x", Decl: blueprint.Loc{File: innerFile, Line: 7, Column: 7}},
	})
}

// setupSettingsFixture builds, under a temp dir:
//
//	modules/a:                backend "local" + output "id"
//	groups/g/group.hcl:       runtime "grouplocal" (line 1); group "g" (line 6)
//	                          with node "own" (runtime attr line 9, approve
//	                          attr line 10) and node "inherit" (nothing)
//	groups/outer2/group.hcl:  group "outer2" instantiating g as "inner" with
//	                          approve = safe (use block line 2, approve line 5)
//	blueprint.hcl:            runtime "tofu" (line 1); use "g" as "plain"
//	                          (line 5; runtime line 8, approve line 9); use
//	                          "outer2" as "nested" (line 12; approve all line 15)
func setupSettingsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/a/main.tf"), `
terraform {
  backend "local" {}
}
output "id" { value = "x" }
`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
runtime "grouplocal" {
  binary  = "grouplocal-binary"
  default = true
}

group "g" {
  node "own" {
    source  = "../../modules/a"
    runtime = runtime.grouplocal
    approve = "all"
  }
  node "inherit" {
    source = "../../modules/a"
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "groups/outer2/group.hcl"), `
group "outer2" {
  use "g" {
    as      = "inner"
    source  = "../g"
    approve = "safe"
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
runtime "tofu" {
  binary = "tofu"
}

use "g" {
  as      = "plain"
  source  = "./groups/g"
  runtime = runtime.tofu
  approve = "safe"
}

use "outer2" {
  as      = "nested"
  source  = "./groups/outer2"
  approve = "all"
}
`)
	return root
}

// TestBuild_SettingSourcesNodeUseUnset covers the three attribution shapes for
// runtime and approve: the node's own attribute, a use-inherited attribute, and
// neither.
func TestBuild_SettingSourcesNodeUseUnset(t *testing.T) {
	root := setupSettingsFixture(t)
	g := parseAndBuild(t, root)
	gFile := filepath.Join(root, "groups/g/group.hcl")
	bpFile := filepath.Join(root, "blueprint.hcl")

	own := g.Nodes["plain.own"]
	if got, want := own.RuntimeSource, (SettingSource{From: "node", Decl: blueprint.Loc{File: gFile, Line: 10, Column: 5}}); got != want {
		t.Fatalf("own runtime source: got %+v, want %+v", got, want)
	}
	if got, want := own.ApproveSource, (SettingSource{From: "node", Decl: blueprint.Loc{File: gFile, Line: 11, Column: 5}}); got != want {
		t.Fatalf("own approve source: got %+v, want %+v", got, want)
	}

	inherit := g.Nodes["plain.inherit"]
	if got, want := inherit.RuntimeSource, (SettingSource{From: "use", Decl: blueprint.Loc{File: bpFile, Line: 9, Column: 3}, UseName: "plain", UseDecl: blueprint.Loc{File: bpFile, Line: 6, Column: 1}}); got != want {
		t.Fatalf("inherited runtime source: got %+v, want %+v", got, want)
	}
	if got, want := inherit.ApproveSource, (SettingSource{From: "use", Decl: blueprint.Loc{File: bpFile, Line: 10, Column: 3}, UseName: "plain", UseDecl: blueprint.Loc{File: bpFile, Line: 6, Column: 1}}); got != want {
		t.Fatalf("inherited approve source: got %+v, want %+v", got, want)
	}

	// Nothing on the nested path named a runtime: the attribution stays empty, and engine's origin layers take over downstream.
	if got := (g.Nodes["nested.inner.inherit"].RuntimeSource); got != (SettingSource{}) {
		t.Fatalf("unset runtime source: got %+v, want the zero value", got)
	}
}

// TestBuild_SettingSourceNestedAttributesEffectiveSetter proves nested-setting
// attribution follows the use whose value the node actually carries: the inner
// use's safe wins over the outer use's all, so the source names the inner use —
// pointing at the outer use's declaration would name a line with no effect.
func TestBuild_SettingSourceNestedAttributesEffectiveSetter(t *testing.T) {
	root := setupSettingsFixture(t)
	g := parseAndBuild(t, root)
	outer2File := filepath.Join(root, "groups/outer2/group.hcl")

	inherit := g.Nodes["nested.inner.inherit"]
	if inherit.Approve != blueprint.ApproveSafe {
		t.Fatalf("the inner use's safe must be the effective value, got %q", inherit.Approve)
	}
	if got, want := inherit.ApproveSource, (SettingSource{From: "use", Decl: blueprint.Loc{File: outer2File, Line: 6, Column: 5}, UseName: "inner", UseDecl: blueprint.Loc{File: outer2File, Line: 3, Column: 3}}); got != want {
		t.Fatalf("nested approve source: got %+v, want %+v", got, want)
	}

	// A node's own attribute beats every enclosing use, however nested.
	if got, want := (g.Nodes["nested.inner.own"].ApproveSource), (SettingSource{From: "node", Decl: blueprint.Loc{File: filepath.Join(root, "groups/g/group.hcl"), Line: 11, Column: 5}}); got != want {
		t.Fatalf("nested own-node approve source: got %+v, want %+v", got, want)
	}
}

// TestGraph_RelationshipsForCycleTerminatesAndExcludesSelf proves the
// relationship lists are deduped, direct and transitive lists agree with
// In/Out, and a node on a cycle neither appears in its own ancestors nor makes
// traversal diverge.
func TestGraph_RelationshipsForCycleTerminatesAndExcludesSelf(t *testing.T) {
	// a -> b (twice: dedupe), b -> c, c -> a (cycle), b -> d (outside the cycle).
	g := newGraph([]string{"a", "b", "c", "d"}, []blueprint.Edge{
		orderEdge("a", "b"),
		dataEdge("a", "out", "b", "in"),
		dataEdge("b", "out", "c", "in"),
		dataEdge("c", "out", "a", "in"),
		dataEdge("b", "out", "d", "in"),
	})

	rel := g.RelationshipsFor("a")
	if got, want := strings.Join(rel.DependsOn, ","), "c"; got != want {
		t.Fatalf("a depends_on: got %q, want %q", got, want)
	}
	if got, want := strings.Join(rel.Dependents, ","), "b"; got != want {
		t.Fatalf("a dependents: got %q, want %q (deduped from two edges)", got, want)
	}
	if got, want := strings.Join(rel.Ancestors, ","), "b,c"; got != want {
		t.Fatalf("a ancestors: got %q, want %q", got, want)
	}
	if got, want := strings.Join(rel.Descendants, ","), "b,c,d"; got != want {
		t.Fatalf("a descendants: got %q, want %q", got, want)
	}
	for _, list := range [][]string{rel.DependsOn, rel.Dependents, rel.Ancestors, rel.Descendants} {
		for _, n := range list {
			if n == "a" {
				t.Fatalf("a on a cycle must be excluded from its own relationships: %+v", rel)
			}
		}
		if !sort.StringsAreSorted(list) {
			t.Fatalf("relationship lists must be sorted: %+v", rel)
		}
	}

	// The cycle peer still reaches a's lists from both directions.
	relC := g.RelationshipsFor("c")
	if got, want := strings.Join(relC.Ancestors, ","), "a,b"; got != want {
		t.Fatalf("c ancestors: got %q, want %q", got, want)
	}
	if got, want := strings.Join(relC.Descendants, ","), "a,b,d"; got != want {
		t.Fatalf("c descendants: got %q, want %q", got, want)
	}
}

// setupClassificationFixture builds one blueprint exercising every input-source
// kind: edge, node_vars (including an explicit null), use_vars (through an
// export), external_or_default, external_required, and every conflict shape
// validate reports (two data edges; data edge + vars; plugin binding + vars).
func setupClassificationFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/vpc/outputs.tf"), `
output "vid" { value = "vpc-123" }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/one/variables.tf"), `
variable "v" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/ownvars/variables.tf"), `
variable "v" { type = string }
variable "n" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/defaulted/variables.tf"), `
variable "d" {
  type    = string
  default = "d"
}
`)
	writeFixtureFile(t, filepath.Join(root, "modules/req/variables.tf"), `
variable "r" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/conflicted/variables.tf"), `
variable "v" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/mixed/variables.tf"), `
variable "v" { type = string }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/pluginsup/variables.tf"), `
variable "s" {
  type      = string
  sensitive = true
}
variable "q" {
  type      = string
  sensitive = true
}
`)
	writeFixtureFile(t, filepath.Join(root, "groups/cls/group.hcl"), `
group "cls" {
  node "leaf" { source = "../../modules/one" }

  export {
    input "v" {
      to = node.leaf.input.v
    }
  }
}
`)
	// blueprint.hcl line map: edge blocks at lines 26, 31, 36, 41.
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./modules/vpc" }

node "edge_supplied" { source = "./modules/one" }

node "own_vars" {
  source = "./modules/ownvars"
  vars = {
    v = "literal"
    n = null
  }
}

node "defaulted" { source = "./modules/defaulted" }

node "required" { source = "./modules/req" }

node "conflicted" { source = "./modules/conflicted" }

node "mixed" {
  source = "./modules/mixed"
  vars = { v = "x" }
}

node "plugin_supplied" {
  source = "./modules/pluginsup"
  input "s" {
    from = plugin.secrets.read
    ref = { id = "x" }
  }
  vars = { q = "y" }
  input "q" {
    from = plugin.secrets.read
    ref = { id = "y" }
  }
}

use "cls" {
  as     = "inst"
  source = "./groups/cls"
  vars = { v = "val" }
}

edge {
  from = node.vpc.output.vid
  to   = node.edge_supplied.input.v
}

edge {
  from = node.vpc.output.vid
  to   = node.conflicted.input.v
}

edge {
  from = node.vpc.output.vid
  to   = node.conflicted.input.v
}

edge {
  from = node.vpc.output.vid
  to   = node.mixed.input.v
}
`)
	return root
}

// TestGraph_InputSourcesFor_ClassifiesEveryKind exercises every classification
// kind on one fixture and proves the conflict classification fires exactly
// where validate's input_conflict errors fire — no more, no less.
func TestGraph_InputSourcesFor_ClassifiesEveryKind(t *testing.T) {
	root := setupClassificationFixture(t)
	g := parseAndBuild(t, root)
	bpFile := filepath.Join(root, "blueprint.hcl")

	src := func(node, input string) InputSource {
		t.Helper()
		s, ok := g.InputSourcesFor(node)[input]
		if !ok {
			t.Fatalf("node.%s.input.%s: no classification (schema key missing?)", node, input)
		}
		return s
	}

	edge := src("edge_supplied", "v")
	if edge.Kind != "edge" || edge.Subject != "node.vpc.output.vid" {
		t.Fatalf("edge classification: got %+v", edge)
	}
	assertLoc(t, "edge supply declaration", edge.Decl, blueprint.Loc{File: bpFile, Line: 44, Column: 1})

	own := src("own_vars", "v")
	if own.Kind != "node_vars" || own.Subject != "node.own_vars.vars.v" {
		t.Fatalf("node_vars classification: got %+v", own)
	}
	// Explicit null stays an explicit supply.
	if null := src("own_vars", "n"); null.Kind != "node_vars" {
		t.Fatalf("explicit null must stay node_vars, got %+v", null)
	}

	useVar := src("inst.leaf", "v")
	if useVar.Kind != "use_vars" || useVar.Subject != "use.inst.vars.v" || len(useVar.MappingPath) != 1 {
		t.Fatalf("use_vars classification: got %+v", useVar)
	}

	if got := src("defaulted", "d").Kind; got != "external_or_default" {
		t.Fatalf("defaulted input: got %q, want external_or_default", got)
	}
	if got := src("required", "r").Kind; got != "external_required" {
		t.Fatalf("required input: got %q, want external_required", got)
	}

	if got := src("plugin_supplied", "s").Kind; got != "plugin_input" {
		t.Fatalf("plugin-bound input: got %q, want plugin_input", got)
	}

	twoEdges := src("conflicted", "v")
	if twoEdges.Kind != "conflict" || len(twoEdges.Candidates) != 2 {
		t.Fatalf("two-data-edge conflict: got %+v", twoEdges)
	}
	for _, c := range twoEdges.Candidates {
		if c.Kind != "edge" {
			t.Fatalf("conflict candidates must carry the individual supplies, got %+v", twoEdges.Candidates)
		}
	}

	mixed := src("mixed", "v")
	if mixed.Kind != "conflict" || len(mixed.Candidates) != 2 || mixed.Candidates[0].Kind != "edge" || mixed.Candidates[1].Kind != "node_vars" {
		t.Fatalf("edge+vars conflict: got %+v", mixed)
	}

	// Conflict classification must mirror validate's input_conflict set exactly.
	classified := map[string]bool{}
	for name := range g.Nodes {
		for input, s := range g.InputSourcesFor(name) {
			if s.Kind == "conflict" {
				classified["node."+name+".input."+input] = true
			}
		}
	}
	reported := map[string]bool{}
	for _, p := range Validate(g) {
		if p.Code == "input_conflict" {
			reported[p.Subject] = true
		}
	}
	if len(classified) != len(reported) {
		t.Fatalf("conflict sets differ: classified %v, validate reports %v", classified, reported)
	}
	for subject := range reported {
		if !classified[subject] {
			t.Fatalf("validate reports conflict on %s but classification does not", subject)
		}
	}
	for subject := range classified {
		if !reported[subject] {
			t.Fatalf("classification reports conflict on %s but validate does not", subject)
		}
	}
}

func findEdge(t *testing.T, g *Graph, fromNode, fromPort, toNode, toPort string) Edge {
	t.Helper()
	for _, e := range g.Edges {
		if e.From.Node == fromNode && e.To.Node == toNode && e.From.Name == fromPort && e.To.Name == toPort {
			return e
		}
	}
	t.Fatalf("no edge %s.%s -> %s.%s in %+v", fromNode, fromPort, toNode, toPort, g.Edges)
	return Edge{}
}
