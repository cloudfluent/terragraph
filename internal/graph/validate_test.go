package graph

import (
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

func TestValidate_MissingOutput(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{
		dataEdge("a", "does_not_exist", "b", "x"),
	})
	g.Nodes["b"].Schema.Variables["x"] = module.Variable{Name: "x"}

	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d: %v", len(problems), problems)
	}
	if !problems[0].IsError() {
		t.Fatalf("expected an Error, got %v", problems[0])
	}
}

func TestValidate_MissingInput(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{
		dataEdge("a", "id", "b", "does_not_exist"),
	})
	g.Nodes["a"].Schema.Outputs["id"] = true

	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d: %v", len(problems), problems)
	}
}

func TestValidate_ValidDataEdge(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{
		dataEdge("a", "id", "b", "x"),
	})
	g.Nodes["a"].Schema.Outputs["id"] = true
	g.Nodes["b"].Schema.Variables["x"] = module.Variable{Name: "x"}

	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected no problems, got %v", problems)
	}
}

func TestValidate_ImplicitEdgeSkipsPortCheck(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{orderEdge("a", "b")})
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected no problems for an ordering-only edge, got %v", problems)
	}
}

func TestValidate_ReportsCycle(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{
		orderEdge("a", "b"),
		orderEdge("b", "a"),
	})
	problems := Validate(g)
	if len(problems) == 0 {
		t.Fatalf("expected a cycle problem to be reported")
	}
	if !problems[0].IsError() {
		t.Fatalf("expected a cycle to be an Error, got %v", problems[0])
	}
}

func TestValidate_ReportsEveryIndependentCycle(t *testing.T) {
	// Two disjoint cyclic clusters: a<->b and c<->d.
	g := newGraph([]string{"a", "b", "c", "d"}, []blueprint.Edge{
		orderEdge("a", "b"),
		orderEdge("b", "a"),
		orderEdge("c", "d"),
		orderEdge("d", "c"),
	})

	problems := Validate(g)
	cycleCount := 0
	for _, p := range problems {
		if p.IsError() {
			cycleCount++
		}
	}
	if cycleCount != 2 {
		t.Fatalf("expected 2 separate cycle problems, got %d: %v", cycleCount, problems)
	}
}

func TestValidate_UnresolvedRequiredVariableIsWarning(t *testing.T) {
	g := newGraph([]string{"a"}, nil)
	g.Nodes["a"].Schema.Variables["region"] = module.Variable{Name: "region", Required: true}

	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d: %v", len(problems), problems)
	}
	if problems[0].IsError() {
		t.Fatalf("expected a Warning, got an Error: %v", problems[0])
	}
}

func TestValidate_RequiredVariableWiredByEdgeHasNoWarning(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{
		dataEdge("a", "id", "b", "x"),
	})
	g.Nodes["a"].Schema.Outputs["id"] = true
	g.Nodes["b"].Schema.Variables["x"] = module.Variable{Name: "x", Required: true}

	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected no problems, got %v", problems)
	}
}

func TestValidate_RequiredVariableSetByVarsHasNoWarning(t *testing.T) {
	g := newGraph([]string{"a"}, nil)
	g.Nodes["a"].Schema.Variables["cidr"] = module.Variable{Name: "cidr", Required: true}
	g.Nodes["a"].Vars = map[string]any{"cidr": "10.16.0.0/20"}

	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected no problems, got %v", problems)
	}
}

func TestValidate_VarsTargetingUnknownVariableIsError(t *testing.T) {
	g := newGraph([]string{"a"}, nil)
	g.Nodes["a"].Vars = map[string]any{"does_not_exist": "x"}

	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d: %v", len(problems), problems)
	}
	if !problems[0].IsError() {
		t.Fatalf("expected an Error, got %v", problems[0])
	}
}

func TestValidate_VarsAndEdgeOnSameInputIsError(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{
		dataEdge("a", "id", "b", "x"),
	})
	g.Nodes["a"].Schema.Outputs["id"] = true
	g.Nodes["b"].Schema.Variables["x"] = module.Variable{Name: "x"}
	g.Nodes["b"].Vars = map[string]any{"x": "literal-value"}

	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d: %v", len(problems), problems)
	}
	if !problems[0].IsError() {
		t.Fatalf("expected an Error, got %v", problems[0])
	}
}

func TestValidate_TwoDataEdgesOnSameInputIsError(t *testing.T) {
	g := newGraph([]string{"vpc", "other", "eks"}, []blueprint.Edge{
		dataEdge("vpc", "vpc_id", "eks", "vpc_id"),
		dataEdge("other", "id", "eks", "vpc_id"),
	})
	g.Nodes["vpc"].Schema.Outputs["vpc_id"] = true
	g.Nodes["other"].Schema.Outputs["id"] = true
	g.Nodes["eks"].Schema.Variables["vpc_id"] = module.Variable{Name: "vpc_id"}

	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d: %v", len(problems), problems)
	}
	if !problems[0].IsError() {
		t.Fatalf("expected an Error, got %v", problems[0])
	}
	want := "node.eks.input.vpc_id: set by more than one data edge; remove extras"
	if problems[0].Message != want {
		t.Fatalf("message = %q, want %q", problems[0].Message, want)
	}
}

func TestValidate_DuplicateDataEdgeIsError(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{
		dataEdge("a", "id", "b", "x"),
		dataEdge("a", "id", "b", "x"),
	})
	g.Nodes["a"].Schema.Outputs["id"] = true
	g.Nodes["b"].Schema.Variables["x"] = module.Variable{Name: "x"}

	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d: %v", len(problems), problems)
	}
	if !problems[0].IsError() {
		t.Fatalf("expected an Error, got %v", problems[0])
	}
	want := "node.b.input.x: set by more than one data edge; remove extras"
	if problems[0].Message != want {
		t.Fatalf("message = %q, want %q", problems[0].Message, want)
	}
}

func TestValidate_ThreeDataEdgesOnSameInputReportsOnce(t *testing.T) {
	g := newGraph([]string{"a", "b", "c", "d"}, []blueprint.Edge{
		dataEdge("a", "id", "d", "x"),
		dataEdge("b", "id", "d", "x"),
		dataEdge("c", "id", "d", "x"),
	})
	g.Nodes["a"].Schema.Outputs["id"] = true
	g.Nodes["b"].Schema.Outputs["id"] = true
	g.Nodes["c"].Schema.Outputs["id"] = true
	g.Nodes["d"].Schema.Variables["x"] = module.Variable{Name: "x"}

	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %d: %v", len(problems), problems)
	}
}

func TestValidate_TwoImplicitEdgesAreNotACollision(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{
		orderEdge("a", "b"),
		orderEdge("a", "b"),
	})
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected no problems for duplicate ordering-only edges, got %v", problems)
	}
}

func TestValidate_OptionalVariableNoWarning(t *testing.T) {
	g := newGraph([]string{"a"}, nil)
	g.Nodes["a"].Schema.Variables["region"] = module.Variable{Name: "region", Required: false}

	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected no problems for an optional variable, got %v", problems)
	}
}
