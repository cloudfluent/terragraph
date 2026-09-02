package engine

import "testing"

// TestExecutionLevels_DottedNodeTarget confirms --node can target a group-internal node's dotted name (e.g. "checkout.cluster"). Group expansion namespaces nodes with plain dots, and node names are treated as opaque map keys everywhere else in the engine, so this needs no special casing.
func TestExecutionLevels_DottedNodeTarget(t *testing.T) {
	e := newTestEngine([]string{"vpc", "checkout.cluster", "checkout.nodegroup"}, nil)

	levels, err := e.executionLevels(Options{Node: "checkout.cluster"}, false)
	if err != nil {
		t.Fatalf("executionLevels: %v", err)
	}
	if len(levels) != 1 || len(levels[0]) != 1 || levels[0][0] != "checkout.cluster" {
		t.Fatalf("expected a single level containing only checkout.cluster, got %v", levels)
	}
}

func TestExecutionLevels_UnknownDottedNodeTarget(t *testing.T) {
	e := newTestEngine([]string{"vpc"}, nil)
	if _, err := e.executionLevels(Options{Node: "checkout.cluster"}, false); err == nil {
		t.Fatalf("expected an error for an unknown node target")
	}
}
