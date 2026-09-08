package engine

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// TestRunLevels_ReportsFailedAndNotRun proves the report covers the whole selection: the failing node is recorded as failed at its level, and every node the aborted run never reached is recorded as not run rather than simply missing.
func TestRunLevels_ReportsFailedAndNotRun(t *testing.T) {
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{orderEdge("a", "b")})
	action := func(name string, applied map[string]exec.Outputs, out io.Writer) (exec.Outputs, string, error) {
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
	action := func(name string, applied map[string]exec.Outputs, out io.Writer) (exec.Outputs, string, error) {
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

func TestRunLevels_ReportsInExecutionOrder(t *testing.T) {
	cases := []struct {
		name           string
		reverse        bool
		failNode       bool
		failAfterLevel bool
	}{
		{name: "success"},
		{name: "destroy order", reverse: true},
		{name: "node failure", failNode: true},
		{name: "afterLevel failure", failAfterLevel: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine([]string{"a", "b", "c", "d", "e", "z"}, []blueprint.Edge{
				orderEdge("d", "a"), orderEdge("e", "b"), orderEdge("z", "c"),
			})
			first, second := []string{"d", "e", "z"}, []string{"a", "b", "c"}
			status := StatusPlanned
			if tc.reverse {
				first, second = second, first
				status = StatusDestroyed
			}

			failure := errors.New("boom")
			lastStarted := make(chan struct{})
			action := func(name string, applied map[string]exec.Outputs, out io.Writer) (exec.Outputs, string, error) {
				// At parallelism 2, the middle node must be recorded before the last can start and release the first, forcing completion order away from name order without sleeps.
				if name == first[0] {
					<-lastStarted
				}
				if name == first[2] {
					close(lastStarted)
				}
				if tc.failNode && name == first[1] {
					return nil, "", failure
				}
				return nil, status, nil
			}
			var afterLevel func() error
			if tc.failAfterLevel {
				afterLevel = func() error { return failure }
			}

			runs, err := e.runLevels(Options{Parallelism: 2}, tc.reverse, action, afterLevel)
			var wantErr error
			if tc.failNode || tc.failAfterLevel {
				wantErr = failure
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("got error = %v, want %v", err, wantErr)
			}

			want := make([]NodeRun, 0, len(first)+len(second))
			for _, name := range first {
				run := NodeRun{Node: name, Level: 1, Status: status}
				if tc.failNode && name == first[1] {
					run.Status, run.Err = StatusFailed, failure
				}
				want = append(want, run)
			}
			for _, name := range second {
				run := NodeRun{Node: name, Level: 2, Status: status}
				if wantErr != nil {
					run.Status = StatusNotRun
				}
				want = append(want, run)
			}
			if !reflect.DeepEqual(runs, want) {
				t.Fatalf("got = %+v, want %+v", runs, want)
			}
		})
	}
}
