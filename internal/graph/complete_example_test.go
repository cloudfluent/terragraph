package graph

import (
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// TestBuild_CompleteExample keeps the large runnable example fully contracted because validation alone permits uncovered data edges.
func TestBuild_CompleteExample(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "examples", "complete"))
	if err != nil {
		t.Fatal(err)
	}
	bp, err := blueprint.ParseDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatal(err)
	}
	g.ContractMode = bp.ContractMode
	if g.ContractMode != "enforce" {
		t.Fatalf("contract mode = %q, want enforce", g.ContractMode)
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if len(g.Nodes) != 63 {
		t.Fatalf("nodes = %d, want 63", len(g.Nodes))
	}
	for _, edge := range g.Edges {
		if !edge.IsDataEdge() {
			continue
		}
		producer := g.Contracts.Lookup(g.Nodes[edge.From.Node].Dir)
		consumer := g.Contracts.Lookup(g.Nodes[edge.To.Node].Dir)
		if producer == nil || consumer == nil {
			t.Fatalf("edge %s -> %s has an uncontracted source", edge.From, edge.To)
		}
		for _, claim := range []blueprint.PortContract{producer.Producer[edge.From.Name], consumer.Consumer[edge.To.Name]} {
			if claim.Type == "" || claim.Nullable == nil || *claim.Nullable || claim.Sensitive == nil {
				t.Fatalf("edge %s -> %s claim = %+v, want typed non-null claim with explicit sensitivity", edge.From, edge.To, claim)
			}
		}
	}
	paths := make(map[string]string)
	for name, node := range g.Nodes {
		want := filepath.Join(dir, ".terragraph", "state", name+".tfstate")
		if got := node.BackendConfig["path"]; got != want {
			t.Fatalf("node %s state path = %q, want %q", name, got, want)
		}
		if other, exists := paths[want]; exists {
			t.Fatalf("nodes %s and %s share state path %q", other, name, want)
		}
		paths[want] = name
	}
}
