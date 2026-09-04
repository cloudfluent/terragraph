package engine

import (
	"bytes"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/graph"
	"github.com/cloudfluent/terragraph/internal/module"
)

// newTestEngine builds an Engine around a graph constructed directly (no real .tf files, no subprocess) so runLevels' scheduling logic can be exercised in isolation.
func newTestEngine(names []string, edges []blueprint.Edge) *Engine {
	g := &graph.Graph{
		Nodes: make(map[string]*graph.Node, len(names)),
		Out:   make(map[string][]string, len(names)),
		In:    make(map[string][]string, len(names)),
	}
	for _, n := range names {
		g.Nodes[n] = &graph.Node{
			Node:   blueprint.Node{Name: n},
			Schema: &module.Schema{Variables: map[string]module.Variable{}, Outputs: map[string]bool{}},
		}
	}
	g.Edges = edges
	for _, e := range edges {
		g.Out[e.From.Node] = append(g.Out[e.From.Node], e.To.Node)
		g.In[e.To.Node] = append(g.In[e.To.Node], e.From.Node)
	}

	return &Engine{
		Graph:  g,
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}
}

func orderEdge(from, to string) blueprint.Edge {
	return blueprint.Edge{From: blueprint.PortRef{Node: from}, To: blueprint.PortRef{Node: to}}
}

func TestRunLevels_RespectsParallelismCap(t *testing.T) {
	// 4 independent nodes, one level, cap concurrency at 2: the number of simultaneously-running actions must never exceed 2.
	e := newTestEngine([]string{"a", "b", "c", "d"}, nil)

	var current, max int64
	action := func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, string, error) {
		n := atomic.AddInt64(&current, 1)
		for {
			m := atomic.LoadInt64(&max)
			if n <= m || atomic.CompareAndSwapInt64(&max, m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt64(&current, -1)
		return nil, "", nil
	}

	if _, err := e.runLevels(Options{Parallelism: 2}, false, action, nil); err != nil {
		t.Fatalf("runLevels: %v", err)
	}
	if max > 2 {
		t.Fatalf("expected at most 2 concurrent actions, saw %d", max)
	}
	if max < 2 {
		t.Fatalf("expected actual concurrency of 2 given 4 independent nodes, saw %d", max)
	}
}

func TestRunLevels_LevelIsABarrier(t *testing.T) {
	// a -> b: b must never start before a finishes, regardless of parallelism.
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{orderEdge("a", "b")})

	var aFinished atomic.Bool
	action := func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, string, error) {
		if name == "a" {
			time.Sleep(30 * time.Millisecond)
			aFinished.Store(true)
			return nil, "", nil
		}
		if name == "b" && !aFinished.Load() {
			return nil, "", fmt.Errorf("b started before a finished")
		}
		return nil, "", nil
	}

	if _, err := e.runLevels(Options{Parallelism: 4}, false, action, nil); err != nil {
		t.Fatalf("runLevels: %v", err)
	}
}

func TestRunLevels_ErrorInLevelStopsNextLevel(t *testing.T) {
	// a -> b, independent c fails alongside a's level; b (the next level) must never run.
	e := newTestEngine([]string{"a", "b", "c"}, []blueprint.Edge{orderEdge("a", "b")})

	var bRan atomic.Bool
	action := func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, string, error) {
		switch name {
		case "c":
			return nil, "", fmt.Errorf("boom")
		case "b":
			bRan.Store(true)
		}
		return nil, "", nil
	}

	_, err := e.runLevels(Options{}, false, action, nil)
	if err == nil {
		t.Fatalf("expected an error")
	}
	if bRan.Load() {
		t.Fatalf("expected the next level not to run after an error")
	}
}

func TestRunLevels_AppliedSnapshotPropagatesAcrossLevels(t *testing.T) {
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{orderEdge("a", "b")})

	action := func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, string, error) {
		if name == "a" {
			return map[string]any{"x": "from-a"}, "", nil
		}
		if applied["a"]["x"] != "from-a" {
			return nil, "", fmt.Errorf("expected b to see a's output, got %v", applied["a"])
		}
		return nil, "", nil
	}

	if _, err := e.runLevels(Options{}, false, action, nil); err != nil {
		t.Fatalf("runLevels: %v", err)
	}
}

func TestRunLevels_AfterLevelRunsOncePerLevel(t *testing.T) {
	e := newTestEngine([]string{"a", "b", "c"}, []blueprint.Edge{orderEdge("a", "b")})
	// levels: [a, c], [b] -> afterLevel should fire twice.

	var calls int64
	action := func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, string, error) {
		return nil, "", nil
	}
	afterLevel := func() error {
		atomic.AddInt64(&calls, 1)
		return nil
	}

	if _, err := e.runLevels(Options{}, false, action, afterLevel); err != nil {
		t.Fatalf("runLevels: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected afterLevel to run twice, ran %d times", calls)
	}
}

func TestRunLevels_AfterLevelErrorAbortsRun(t *testing.T) {
	e := newTestEngine([]string{"a", "b"}, []blueprint.Edge{orderEdge("a", "b")})

	var bRan atomic.Bool
	action := func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, string, error) {
		if name == "b" {
			bRan.Store(true)
		}
		return nil, "", nil
	}
	afterLevel := func() error { return fmt.Errorf("save failed") }

	if _, err := e.runLevels(Options{}, false, action, afterLevel); err == nil {
		t.Fatalf("expected an error from afterLevel to abort the run")
	}
	if bRan.Load() {
		t.Fatalf("expected level 2 not to run after afterLevel errors on level 1")
	}
}
