package engine

import (
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

func dataEdge(fromNode, fromOutput, toNode, toInput string) blueprint.Edge {
	return blueprint.Edge{
		From: blueprint.PortRef{Node: fromNode, Kind: blueprint.PortOutput, Name: fromOutput},
		To:   blueprint.PortRef{Node: toNode, Kind: blueprint.PortInput, Name: toInput},
	}
}

func TestCheckType_MatchingTypePasses(t *testing.T) {
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{dataEdge("a", "id", "b", "vpc_id")})
	e.Graph.Nodes["b"].Schema.Variables["vpc_id"] = module.Variable{Name: "vpc_id", Type: "string"}

	edge := dataEdge("a", "id", "b", "vpc_id")
	if err := e.checkType(edge, "vpc-123"); err != nil {
		t.Fatalf("expected no error for a matching string value, got %v", err)
	}
}

func TestCheckType_MismatchedTypeFails(t *testing.T) {
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{dataEdge("a", "id", "b", "subnet_ids")})
	e.Graph.Nodes["b"].Schema.Variables["subnet_ids"] = module.Variable{Name: "subnet_ids", Type: "list(string)"}

	edge := dataEdge("a", "id", "b", "subnet_ids")
	if err := e.checkType(edge, "vpc-123"); err == nil {
		t.Fatalf("expected an error feeding a string into a list(string) variable")
	}
}

func TestCheckType_ListValueIntoListTypePasses(t *testing.T) {
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{dataEdge("a", "ids", "b", "subnet_ids")})
	e.Graph.Nodes["b"].Schema.Variables["subnet_ids"] = module.Variable{Name: "subnet_ids", Type: "list(string)"}

	edge := dataEdge("a", "ids", "b", "subnet_ids")
	val := []any{"subnet-1", "subnet-2"}
	if err := e.checkType(edge, val); err != nil {
		t.Fatalf("expected no error for a matching list value, got %v", err)
	}
}

func TestCheckType_UntypedVariableAlwaysPasses(t *testing.T) {
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{dataEdge("a", "id", "b", "anything")})
	e.Graph.Nodes["b"].Schema.Variables["anything"] = module.Variable{Name: "anything", Type: ""}

	edge := dataEdge("a", "id", "b", "anything")
	if err := e.checkType(edge, map[string]any{"nested": true}); err != nil {
		t.Fatalf("expected no error for an untyped variable, got %v", err)
	}
}
