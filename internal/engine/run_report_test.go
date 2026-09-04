package engine

import (
	"fmt"
	"io"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// TestRunLevels_ReportsFailedAndNotRun proves the report covers the whole selection: the failing node is recorded as failed at its level, and every node the aborted run never reached is recorded as not run rather than simply missing.
func TestRunLevels_ReportsFailedAndNotRun(t *testing.T) {
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{orderEdge("a", "b")})
	action := func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, string, error) {
		if name == "a" {
			return nil, "", fmt.Errorf("boom")
		}
		return nil, StatusPlanned, nil
	}

	runs, err := e.runLevels(Options{}, false, action, nil)
	if err == nil {
		t.Fatal("expected the failing node to fail the run")
	}
	if len(runs) != 2 {
		t.Fatalf("got = %d runs, want 2 (failed plus not run): %v", len(runs), runs)
	}
	if runs[0].Node != "a" || runs[0].Level != 1 || runs[0].Status != StatusFailed || runs[0].Err == nil {
		t.Fatalf("got = %+v, want a/1/failed with error", runs[0])
	}
	if runs[1].Node != "b" || runs[1].Level != 2 || runs[1].Status != StatusNotRun || runs[1].Err != nil {
		t.Fatalf("got = %+v, want b/2/not run with no error", runs[1])
	}
}

// TestRunLevels_ReportsSuccessStatuses proves the status an action returns is what the report carries, level numbers follow execution order, and a successful run reports every selected node.
func TestRunLevels_ReportsSuccessStatuses(t *testing.T) {
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{orderEdge("a", "b")})
	action := func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, string, error) {
		return nil, StatusPlanned, nil
	}

	runs, err := e.runLevels(Options{}, false, action, nil)
	if err != nil {
		t.Fatalf("runLevels: %v", err)
	}
	if len(runs) != 2 || runs[0].Status != StatusPlanned || runs[1].Status != StatusPlanned {
		t.Fatalf("got = %v, want both nodes planned", runs)
	}
	if runs[0].Level != 1 || runs[1].Level != 2 {
		t.Fatalf("got levels = %d,%d, want 1,2", runs[0].Level, runs[1].Level)
	}
}
