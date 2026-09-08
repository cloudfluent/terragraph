package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// selectionFixture uses real modules and a diamond so both dependency expansion and external readers are observable without a runtime.
func selectionFixture(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	if err := osWriteFile(filepath.Join(dir, "module", "main.tf"), []byte("terraform {\n backend \"local\" {}\n}\nvariable \"value\" { default = \"\" }\noutput \"value\" { value = \"\" }")); err != nil {
		t.Fatal(err)
	}
	path := writeBlueprint(t, dir, `
node "root" { source = "./module" }
node "left" { source = "./module" }
node "right" { source = "./module" }
node "leaf" { source = "./module" }
node "other" { source = "./module" }
edge {
 from = node.root.output.value
 to = node.left.input.value
}
edge {
 from = node.root
 to = node.right
}
edge {
 from = node.left
 to = node.leaf
}
edge {
 from = node.right
 to = node.leaf
}
`)
	e, err := Load(path, exec.Binary(filepath.Join(dir, "must-not-start")), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestPreview_ExpandsOnlyExplicitRoots(t *testing.T) {
	e := selectionFixture(t)
	p, err := e.Preview(Options{Nodes: []string{"left", "left"}, IncludeDependencies: true, IncludeDependents: true}, "apply")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, node := range p.Nodes {
		names = append(names, node.Node)
	}
	if !reflect.DeepEqual(names, []string{"root", "left", "leaf"}) {
		t.Fatalf("nodes = %v, want only ancestors and descendants of left", names)
	}
	if p.Nodes[0].Reason != "dependency" || p.Nodes[1].Reason != "explicit" || p.Nodes[2].Reason != "dependent" {
		t.Fatalf("reasons = %+v", p.Nodes)
	}
	if !reflect.DeepEqual(p.Nodes[2].ExternalPrerequisites, []string{"right"}) {
		t.Fatalf("external prerequisites = %v, want right", p.Nodes[2].ExternalPrerequisites)
	}
	if _, err := os.Stat(filepath.Join(e.BaseDir, ".terragraph")); !os.IsNotExist(err) {
		t.Fatalf("preview created execution state: %v", err)
	}
}

func TestPreview_ReportsExternalInputAndDestroyImpact(t *testing.T) {
	e := selectionFixture(t)
	p, err := e.Preview(Options{Node: "left"}, "destroy")
	if err != nil {
		t.Fatal(err)
	}
	if p.DestroyScopeComplete || !reflect.DeepEqual(p.OutsideDependents, []string{"leaf"}) {
		t.Fatalf("impact = %+v", p)
	}
	if len(p.Nodes[0].ExternalInputs) != 1 || p.Nodes[0].ExternalInputs[0].Node != "root" {
		t.Fatalf("external inputs = %+v", p.Nodes[0])
	}
	if err := e.checkDestroyScope(Options{Node: "left"}); err == nil || !strings.Contains(err.Error(), "--include-dependents") {
		t.Fatalf("scope error = %v", err)
	}
	if err := e.checkDestroyScope(Options{Node: "left", IncludeDependents: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.checkDestroyScope(Options{Node: "left", AllowOrphanDestroy: true}); err != nil {
		t.Fatal(err)
	}
}

func TestPreview_MultipleTargetsAndReverseOrder(t *testing.T) {
	e := selectionFixture(t)
	p, err := e.Preview(Options{Nodes: []string{"root", "other"}, IncludeDependents: true}, "destroy")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != 5 || !p.DestroyScopeComplete || p.Nodes[len(p.Nodes)-1].Node != "root" {
		t.Fatalf("preview = %+v", p)
	}
}

func TestRunLevels_ReadyChildDoesNotWaitForUnrelatedRoot(t *testing.T) {
	e := newTestEngine([]string{"a", "b", "child"}, []blueprint.Edge{orderEdge("a", "child")})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e.Context = ctx
	childRan := make(chan struct{})
	action := func(ctx context.Context, name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if name == "b" {
			select {
			case <-childRan:
			case <-ctx.Done():
				return nil, "", ctx.Err()
			}
		}
		if name == "child" {
			close(childRan)
		}
		return nil, StatusApplied, nil
	}
	runs, err := e.runLevels(Options{Parallelism: 2}, false, action, nil)
	if err != nil {
		t.Fatalf("runs = %+v, error = %v, want child to unblock b", runs, err)
	}
}

func TestRunLevels_KeepGoingBlocksOnlyFailedDescendants(t *testing.T) {
	e := newTestEngine([]string{"a", "b", "c", "d", "e"}, []blueprint.Edge{orderEdge("a", "c"), orderEdge("c", "e"), orderEdge("b", "d")})
	failure := errors.New("fixture failure")
	action := func(_ context.Context, name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if name == "a" {
			return nil, "", failure
		}
		if name == "c" || name == "e" {
			t.Errorf("blocked descendant %s started", name)
		}
		return nil, StatusApplied, nil
	}
	runs, err := e.runLevels(Options{Parallelism: 2, KeepGoing: true}, false, action, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want original failure", err)
	}
	for _, run := range runs {
		if run.Node == "c" || run.Node == "e" {
			if run.Reason != "dependency_failed" || !reflect.DeepEqual(run.BlockedBy, []string{"a"}) || run.Status != StatusNotRun {
				t.Fatalf("blocked report = %+v", run)
			}
		}
		if run.Node == "d" && run.Status != StatusApplied {
			t.Fatalf("independent descendant = %+v", run)
		}
	}
}

func TestRunLevels_FailFastStopsQueuedNodes(t *testing.T) {
	e := newTestEngine([]string{"a", "b", "c"}, nil)
	action := func(_ context.Context, name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if name != "a" {
			t.Errorf("queued node %s started", name)
		}
		return nil, "", errors.New("stop")
	}
	runs, err := e.runLevels(Options{}, false, action, nil)
	if err == nil || runs[1].Reason != "fail_fast" || runs[2].Status != StatusNotRun {
		t.Fatalf("runs = %+v, err = %v", runs, err)
	}
}

func TestRunLevels_ReverseFailureProtectsAncestors(t *testing.T) {
	e := newTestEngine([]string{"a", "b", "other"}, []blueprint.Edge{orderEdge("a", "b")})
	action := func(_ context.Context, name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if name == "a" {
			t.Error("destroyed parent after consumer failure")
		}
		if name == "b" {
			return nil, "", errors.New("delete failed")
		}
		return nil, StatusDestroyed, nil
	}
	runs, err := e.runLevels(Options{KeepGoing: true}, true, action, nil)
	if err == nil || runs[1].Reason != "dependency_failed" {
		t.Fatalf("runs = %+v, err = %v", runs, err)
	}
}

func TestRunLevels_NodeTimeoutKeepsIndependentBranch(t *testing.T) {
	e := newTestEngine([]string{"a", "b", "child"}, []blueprint.Edge{orderEdge("a", "child")})
	action := func(ctx context.Context, name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if name == "a" {
			<-ctx.Done()
			return nil, "", ctx.Err()
		}
		return nil, StatusApplied, nil
	}
	runs, err := e.runLevels(Options{Parallelism: 2, KeepGoing: true, Timeouts: map[string]time.Duration{"a": 20 * time.Millisecond}}, false, action, nil)
	if !errors.Is(err, context.DeadlineExceeded) || runs[0].Reason != "timeout" || runs[0].Duration <= 0 || runs[1].Status != StatusApplied || runs[2].Reason != "dependency_failed" {
		t.Fatalf("runs = %+v, err = %v", runs, err)
	}
}

func TestRunLevels_PoolsDoNotBlockUnrelatedWork(t *testing.T) {
	e := newTestEngine([]string{"a", "b", "c"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e.Context = ctx
	release := make(chan struct{})
	var inPool atomic.Int32
	action := func(ctx context.Context, name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
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
