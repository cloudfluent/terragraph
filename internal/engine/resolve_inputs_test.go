package engine

import (
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

func TestResolveInputs_MergesLiteralVars(t *testing.T) {
	e := newTestEngine([]string{"a"}, nil)
	e.Graph.Nodes["a"].Schema.Variables["cidr"] = module.Variable{Name: "cidr", Type: "string"}
	e.Graph.Nodes["a"].Schema.Variables["az_count"] = module.Variable{Name: "az_count", Type: "number"}
	e.Graph.Nodes["a"].Vars = map[string]any{"cidr": "10.16.0.0/20", "az_count": float64(3)}

	vars, err := e.resolveInputs("a", nil)
	if err != nil {
		t.Fatalf("resolveInputs: %v", err)
	}
	if vars["cidr"] != "10.16.0.0/20" {
		t.Fatalf("unexpected cidr: %+v", vars["cidr"])
	}
	if vars["az_count"] != float64(3) {
		t.Fatalf("unexpected az_count: %+v", vars["az_count"])
	}
}

func TestResolveInputs_EdgeAndVarsTogether(t *testing.T) {
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{dataEdge("a", "id", "b", "vpc_id")})
	e.Graph.Nodes["b"].Schema.Variables["vpc_id"] = module.Variable{Name: "vpc_id", Type: "string"}
	e.Graph.Nodes["b"].Schema.Variables["cidr"] = module.Variable{Name: "cidr", Type: "string"}
	e.Graph.Nodes["b"].Vars = map[string]any{"cidr": "10.16.0.0/20"}

	applied := map[string]map[string]any{"a": {"id": "vpc-123"}}
	vars, err := e.resolveInputs("b", applied)
	if err != nil {
		t.Fatalf("resolveInputs: %v", err)
	}
	if vars["vpc_id"] != "vpc-123" {
		t.Fatalf("expected the edge-resolved value to still be present, got %+v", vars["vpc_id"])
	}
	if vars["cidr"] != "10.16.0.0/20" {
		t.Fatalf("expected the vars-supplied value to be present, got %+v", vars["cidr"])
	}
}

func TestResolveInputs_ConflictBetweenEdgeAndVarsErrors(t *testing.T) {
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{dataEdge("a", "id", "b", "x")})
	e.Graph.Nodes["b"].Schema.Variables["x"] = module.Variable{Name: "x", Type: "string"}
	e.Graph.Nodes["b"].Vars = map[string]any{"x": "literal-value"}

	applied := map[string]map[string]any{"a": {"id": "vpc-123"}}
	if _, err := e.resolveInputs("b", applied); err == nil {
		t.Fatalf("expected an error: %q is set by both an edge and vars", "x")
	}
}

func TestResolveInputs_VarsValueFailsTypeCheck(t *testing.T) {
	e := newTestEngine([]string{"a"}, nil)
	e.Graph.Nodes["a"].Schema.Variables["subnet_ids"] = module.Variable{Name: "subnet_ids", Type: "list(string)"}
	e.Graph.Nodes["a"].Vars = map[string]any{"subnet_ids": "not-a-list"}

	if _, err := e.resolveInputs("a", nil); err == nil {
		t.Fatalf("expected an error feeding a string into a list(string) variable via vars")
	}
}
