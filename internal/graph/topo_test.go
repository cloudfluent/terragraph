package graph

import (
	"reflect"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func indexIn(order []string, name string) int {
	for i, n := range order {
		if n == name {
			return i
		}
	}
	return -1
}

func TestTopoSort_Linear(t *testing.T) {
	g := newGraph([]string{"a", "b", "c"}, []blueprint.Edge{
		orderEdge("a", "b"),
		orderEdge("b", "c"),
	})

	order, err := TopoSort(g)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"a", "b", "c"}) {
		t.Fatalf("unexpected order: %v", order)
	}
}

func TestTopoSort_Diamond(t *testing.T) {
	g := newGraph([]string{"a", "b", "c", "d"}, []blueprint.Edge{
		orderEdge("a", "b"),
		orderEdge("a", "c"),
		orderEdge("b", "d"),
		orderEdge("c", "d"),
	})

	order, err := TopoSort(g)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if indexIn(order, "a") != 0 {
		t.Fatalf("expected a first, got order %v", order)
	}
	if indexIn(order, "d") != 3 {
		t.Fatalf("expected d last, got order %v", order)
	}
	if indexIn(order, "b") > indexIn(order, "d") || indexIn(order, "c") > indexIn(order, "d") {
		t.Fatalf("b and c must both precede d, got order %v", order)
	}
}

func TestTopoSort_Cycle(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{
		orderEdge("a", "b"),
		orderEdge("b", "a"),
	})

	if _, err := TopoSort(g); err == nil {
		t.Fatalf("expected an error for a cyclic graph")
	}
}

func TestFindCycle_ReturnsMembers(t *testing.T) {
	g := newGraph([]string{"a", "b", "c"}, []blueprint.Edge{
		orderEdge("a", "b"),
		orderEdge("b", "c"),
		orderEdge("c", "a"),
	})

	cycle := FindCycle(g)
	if !reflect.DeepEqual(cycle, []string{"a", "b", "c"}) {
		t.Fatalf("expected all 3 nodes in the cyclic SCC, got %v", cycle)
	}
}

func TestFindCycle_Acyclic(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{orderEdge("a", "b")})
	if cycle := FindCycle(g); cycle != nil {
		t.Fatalf("expected no cycle, got %v", cycle)
	}
}

func TestLevels_Linear(t *testing.T) {
	g := newGraph([]string{"a", "b", "c"}, []blueprint.Edge{
		orderEdge("a", "b"),
		orderEdge("b", "c"),
	})

	levels, err := Levels(g)
	if err != nil {
		t.Fatalf("Levels: %v", err)
	}
	want := [][]string{{"a"}, {"b"}, {"c"}}
	if !reflect.DeepEqual(levels, want) {
		t.Fatalf("expected %v, got %v", want, levels)
	}
}

func TestLevels_Diamond(t *testing.T) {
	g := newGraph([]string{"a", "b", "c", "d"}, []blueprint.Edge{
		orderEdge("a", "b"),
		orderEdge("a", "c"),
		orderEdge("b", "d"),
		orderEdge("c", "d"),
	})

	levels, err := Levels(g)
	if err != nil {
		t.Fatalf("Levels: %v", err)
	}
	want := [][]string{{"a"}, {"b", "c"}, {"d"}}
	if !reflect.DeepEqual(levels, want) {
		t.Fatalf("expected %v, got %v", want, levels)
	}
}

func TestLevels_Cycle(t *testing.T) {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{
		orderEdge("a", "b"),
		orderEdge("b", "a"),
	})
	if _, err := Levels(g); err == nil {
		t.Fatalf("expected an error for a cyclic graph")
	}
}
