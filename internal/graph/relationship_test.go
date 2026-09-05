package graph

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func TestLevels_RelationshipDoesNotCreateExecutionDependency(t *testing.T) {
	g := newGraph([]string{"a", "b"}, nil)
	g.Relationships = []blueprint.Relationship{{Left: "a", Right: "b"}}

	levels, err := Levels(g)
	if err != nil {
		t.Fatalf("Levels: %v", err)
	}
	if len(levels) != 1 || strings.Join(levels[0], ",") != "a,b" {
		t.Fatalf("levels = %v, want both nodes in one level", levels)
	}
}

func TestValidate_RelationshipWithoutContractsReportsC010(t *testing.T) {
	g := newGraph([]string{"a", "b"}, nil)
	g.Relationships = []blueprint.Relationship{{Left: "a", Right: "b"}}

	problems := Validate(g)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want one per endpoint", problems)
	}
	for _, problem := range problems {
		if problem.IsError() || !strings.Contains(problem.Message, "[C010]") {
			t.Fatalf("problem = %+v, want C010 warning", problem)
		}
	}
}

func TestValidate_RelationshipWithoutContractsEnforceIsError(t *testing.T) {
	g := newGraph([]string{"a", "b"}, nil)
	g.Relationships = []blueprint.Relationship{{Left: "a", Right: "b"}}
	g.ContractMode = "enforce"

	problems := Validate(g)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want one per endpoint", problems)
	}
	for _, problem := range problems {
		if !strings.Contains(problem.Message, "[C010]") || !problem.IsError() {
			t.Fatalf("problem = %+v, want error", problem)
		}
	}
}

func TestValidate_RelationshipWithContractsHasNoC010(t *testing.T) {
	g := newGraph([]string{"a", "b"}, nil)
	g.Nodes["a"].Source, g.Nodes["a"].Dir = "./a", "/modules/a"
	g.Nodes["b"].Source, g.Nodes["b"].Dir = "./b", "/modules/b"
	g.Relationships = []blueprint.Relationship{{Left: "a", Right: "b"}}
	g.Contracts = &blueprint.Contracts{ByDir: map[string]*blueprint.DirContracts{
		"/modules/a": {Scope: "./a", Dir: "/modules/a"},
		"/modules/b": {Scope: "./b", Dir: "/modules/b"},
	}}

	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("problems = %v, want contracted relationship to be valid", problems)
	}
}

func TestBuild_GroupRelationshipsAreNamespacedPerUse(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "modules/a/main.tf"), ``)
	writeFixtureFile(t, filepath.Join(root, "modules/b/main.tf"), ``)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  node "b" { source = "../../modules/b" }
  relationship { between = [node.a, node.b] }
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

	g := parseAndBuild(t, root)
	got := SortedRelationships(g)
	want := []blueprint.Relationship{
		{Left: "first.a", Right: "first.b"},
		{Left: "second.a", Right: "second.b"},
	}
	if len(got) != len(want) {
		t.Fatalf("relationships = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("relationships = %+v, want %+v", got, want)
		}
	}
	if len(g.Edges) != 0 || len(g.Out) != 0 || len(g.In) != 0 {
		t.Fatalf("relationship changed execution graph: edges=%v out=%v in=%v", g.Edges, g.Out, g.In)
	}
}

func TestRelationshipDOT_IsUndirectedAndSorted(t *testing.T) {
	g := newGraph([]string{"b", "a"}, nil)
	g.Relationships = []blueprint.Relationship{{Left: "b", Right: "a"}}
	want := "graph terragraph_relationships {\n  rankdir=LR;\n  \"a\";\n  \"b\";\n  \"a\" -- \"b\";\n}\n"
	if got := RelationshipDOT(g); got != want {
		t.Fatalf("DOT = %q, want %q", got, want)
	}
}
