package engine

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// dagFixture loads two independent branches so dispatch can be proved without timing assumptions about subprocesses.
func dagFixture(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	if err := osWriteFile(filepath.Join(dir, "module", "main.tf"), []byte("terraform {\n backend \"local\" {}\n}\nvariable \"value\" { default = \"\" }\noutput \"value\" { value = \"\" }")); err != nil {
		t.Fatal(err)
	}
	path := writeBlueprint(t, dir, `
node "a" { source = "./module" }
node "b" { source = "./module" }
node "child" { source = "./module" }
node "slow" { source = "./module" }
edge {
 from = node.a.output.value
 to = node.child.input.value
}
edge {
 from = node.b
 to = node.slow
}
`)
	e, err := Load(path, exec.Binary(filepath.Join(dir, "must-not-start")), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	e.Context = ctx
	return e
}

func TestRunLevels_ReadyChildDoesNotWaitForUnrelatedRoot(t *testing.T) {
	e := dagFixture(t)
	childRan := make(chan struct{})
	runs, err := e.runLevels(Options{Parallelism: 2}, false, func(name string, applied map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		switch name {
		case "a":
			return exec.Outputs{"value": {Value: "fresh"}}, StatusApplied, nil
		case "b":
			select {
			case <-childRan:
			case <-e.context().Done():
				return nil, "", e.context().Err()
			}
		case "child":
			if applied["a"]["value"].Value != "fresh" {
				t.Errorf("outputs = %v, want fresh upstream value", applied)
			}
			close(childRan)
		}
		return nil, StatusApplied, nil
	}, nil)
	if err != nil {
		t.Fatalf("runs = %+v, error = %v, want child to unblock b", runs, err)
	}
	if len(runs) != 4 || runs[0].Node != "a" || runs[2].Node != "child" || runs[2].Level != 2 {
		t.Fatalf("runs = %+v, want stable level/name order", runs)
	}
}

func TestRunLevels_ReadyDestroyDoesNotWaitForUnrelatedConsumer(t *testing.T) {
	e := dagFixture(t)
	parentRan := make(chan struct{})
	runs, err := e.runLevels(Options{Parallelism: 2}, true, func(name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if name == "slow" {
			select {
			case <-parentRan:
			case <-e.context().Done():
				return nil, "", e.context().Err()
			}
		}
		if name == "a" {
			close(parentRan)
		}
		return nil, StatusDestroyed, nil
	}, nil)
	if err != nil {
		t.Fatalf("runs = %+v, error = %v, want a to unblock slow", runs, err)
	}
	if runs[0].Node != "child" || runs[0].Level != 1 || runs[2].Node != "a" || runs[2].Level != 2 {
		t.Fatalf("runs = %+v, want reverse level labels", runs)
	}
}

func TestRunLevels_ExactSelectionDoesNotWaitForExternalParent(t *testing.T) {
	e := dagFixture(t)
	runs, err := e.runLevels(Options{Nodes: []string{"child"}, Parallelism: 2}, false, func(name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if name != "child" {
			t.Errorf("node = %q, want child", name)
		}
		return nil, StatusPlanned, nil
	}, nil)
	if err != nil || len(runs) != 1 || runs[0].Status != StatusPlanned {
		t.Fatalf("runs = %+v, error = %v, want only child planned", runs, err)
	}
}

func TestRunLevels_ReviewPreservesDependencyDiagnostics(t *testing.T) {
	e := dagFixture(t)
	failure := errors.New("fixture failure")
	runs, err := e.runLevels(Options{Parallelism: 2}, false, func(name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if name == "a" {
			return nil, "", failure
		}
		if name == "child" {
			t.Error("failed dependency's child started")
		}
		return nil, StatusPlanned, nil
	}, nil, true)
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want %v", err, failure)
	}
	if runs[2].Status != StatusNotRun || !errors.Is(runs[2].Err, errPlanBlocked) || len(runs[2].Diagnostics) != 1 || runs[3].Status != StatusPlanned {
		t.Fatalf("runs = %+v, want blocked diagnostic and successful independent branch", runs)
	}
}

func TestRunLevels_FailureLetsQueuedSiblingsFinish(t *testing.T) {
	e := dagFixture(t)
	failure := errors.New("fixture failure")
	runs, err := e.runLevels(Options{}, false, func(name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if name == "a" {
			return nil, "", failure
		}
		if name != "b" {
			t.Errorf("node = %q, want queued sibling b only", name)
		}
		return nil, StatusApplied, nil
	}, nil)
	if !errors.Is(err, failure) || runs[1].Status != StatusApplied || runs[2].Status != StatusNotRun || runs[3].Status != StatusNotRun {
		t.Fatalf("runs = %+v, error = %v, want existing failure boundary", runs, err)
	}
}

func TestRunLevels_OverlappingFailurePreservesExecutionJournal(t *testing.T) {
	e := dagFixture(t)
	opts, err := e.resolveSelection(Options{Parallelism: 2})
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := e.lockRun()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	session, err := e.startExecution("apply", opts, false)
	if err != nil {
		t.Fatal(err)
	}
	defer session.close()
	childStarted, failureRecorded := make(chan struct{}), make(chan struct{})
	failure := errors.New("fixture mutation failed")
	runs, runErr := e.runLevels(opts, false, func(name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if err := session.transition(name, "operating", "", ""); err != nil {
			return nil, "", err
		}
		switch name {
		case "b":
			select {
			case <-childStarted:
			case <-e.context().Done():
				return nil, "", e.context().Err()
			}
			err := session.fail(name, "indeterminate", failure)
			close(failureRecorded)
			return nil, "", err
		case "child":
			close(childStarted)
			select {
			case <-failureRecorded:
			case <-e.context().Done():
				return nil, "", e.context().Err()
			}
		case "slow":
			t.Error("failed producer's consumer started")
		}
		return nil, StatusApplied, session.transition(name, "completed", "", "")
	}, nil)
	if !errors.Is(runErr, failure) || runs[2].Status != StatusApplied || runs[3].Status != StatusNotRun {
		t.Fatalf("runs = %+v, error = %v, want running child to finish", runs, runErr)
	}
	if err := session.finish(runErr); err != nil {
		t.Fatal(err)
	}
	record, _, err := readExecutionRecord(e.context(), session.store, session.record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "needs_recovery" || record.Nodes[1].Phase != "indeterminate" || record.Nodes[2].Phase != "completed" || record.Nodes[3].Phase != "pending" {
		t.Fatalf("record = %+v, want preserved completion and explicit recovery", record)
	}
}

func TestRunLevels_PoolsDoNotBlockUnrelatedWork(t *testing.T) {
	e := newTestEngine([]string{"a", "b", "c"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e.Context = ctx
	release := make(chan struct{})
	var inPool atomic.Int32
	action := func(name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if name == "c" {
			close(release)
			return nil, StatusApplied, nil
		}
		if n := inPool.Add(1); n != 1 {
			t.Errorf("pool users = %d, want 1", n)
		}
		defer inPool.Add(-1)
		if name == "a" {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, "", ctx.Err()
			}
		}
		return nil, StatusApplied, nil
	}
	runs, err := e.runLevels(Options{Parallelism: 2, Pools: []ConcurrencyPool{{Name: "account", Limit: 1, Nodes: []string{"a", "b"}}, {Name: "api", Limit: 1, Nodes: []string{"a", "b"}}}}, false, action, nil)
	if err != nil {
		t.Fatalf("runs = %+v, err = %v", runs, err)
	}
}

func TestApply_PoolCannotSilentlyReferenceUnknownLeaf(t *testing.T) {
	e := dagFixture(t)
	_, err := e.Apply(Options{AutoApprove: true, Pools: []ConcurrencyPool{{Name: "api", Limit: 1, Nodes: []string{"missing"}}}})
	if err == nil || !strings.Contains(err.Error(), "unknown or repeated node") {
		t.Fatalf("got = %v, want invalid pool rejected before runtime", err)
	}
}
