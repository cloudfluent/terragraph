package engine

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/graph"
)

// Options controls the scope and execution behavior of a plan/apply/destroy run.
type Options struct {
	// Node restricts the operation to a single node. Empty means the whole graph, in topological (or, for Destroy, reverse topological) order.
	Node string
	// AutoApprove skips the interactive approval Apply would otherwise ask for, and is forwarded to `terraform destroy` as -auto-approve. It governs only whether a human is asked; what a node is permitted to do unattended is Approve's job, and the two are checked independently.
	AutoApprove bool
	// Approve is the run-wide default approve level (see blueprint.Approve) for nodes that declare none of their own. Empty means blueprint.ApproveSafe.
	Approve blueprint.Approve
	// Parallelism caps how many nodes within one execution level run concurrently. <=1 means sequential (the default), matching v1 behavior and avoiding surprising provider API rate-limit issues.
	Parallelism int
}

func (o Options) parallelism() int {
	if o.Parallelism < 1 {
		return 1
	}
	return o.Parallelism
}

func (e *Engine) executionLevels(opts Options, reverse bool) ([][]string, error) {
	if opts.Node != "" {
		if _, ok := e.Graph.Nodes[opts.Node]; !ok {
			return nil, fmt.Errorf("unknown node %q", opts.Node)
		}
		return [][]string{{opts.Node}}, nil
	}

	levels, err := graph.Levels(e.Graph)
	if err != nil {
		return nil, err
	}
	if reverse {
		for i, j := 0, len(levels)-1; i < j; i, j = i+1, j-1 {
			levels[i], levels[j] = levels[j], levels[i]
		}
	}
	return levels, nil
}

// Statuses a NodeRun can carry. The per-command success values (planned, applied, unchanged, destroyed) are chosen by the run that produced them; failed and not run are runLevels' own verdicts.
const (
	StatusPlanned   = "planned"   // plan ran to completion
	StatusApplied   = "applied"   // apply made changes
	StatusUnchanged = "unchanged" // apply skipped the node: its plan reported no changes
	StatusDestroyed = "destroyed" // destroy ran to completion
	StatusFailed    = "failed"    // the node's own step returned an error
	StatusNotRun    = "not run"   // an earlier level failed, so the run never reached this node
)

// NodeRun records one node's outcome in a plan/apply/destroy run. Level is 1-based in execution order (reversed for destroy), so a caller can present results in run order without re-deriving the graph; Err is the node's own error, without the node %q prefix runLevels adds when failing the run.
type NodeRun struct {
	Node   string
	Level  int
	Status string
	Err    error
}

// nodeAction runs one node's step of a plan/apply/destroy: given the outputs applied so far this run and a writer for this node's terraform output, it returns the outputs to feed downstream (nil if the node produced none worth propagating, e.g. Destroy), the success status to record for the node, and an error.
type nodeAction func(name string, applied map[string]map[string]any, out io.Writer) (outputs map[string]any, status string, err error)

// runLevels is the shared execution loop behind Plan/Apply/Destroy: it walks the graph (or a single node) level by level, running up to opts.Parallelism nodes within a level concurrently. Nodes in the same level are guaranteed to have no edge between them, so a read-only snapshot of outputs applied so far is safe to share across the level's goroutines, and results are merged back only once the whole level completes (no data races). If any node in a level errors, already-started siblings finish but the next level never starts; the returned runs record those unreached nodes as StatusNotRun so a report covers the whole selection rather than stopping where execution did. afterLevel, if non-nil, runs once each level completes successfully; an error from it aborts the run the same way.
func (e *Engine) runLevels(opts Options, reverse bool, action nodeAction, afterLevel func() error) ([]NodeRun, error) {
	levels, err := e.executionLevels(opts, reverse)
	if err != nil {
		return nil, err
	}

	applied := map[string]map[string]any{}
	var mu sync.Mutex
	var outMu sync.Mutex
	buffered := opts.parallelism() > 1
	runs := make([]NodeRun, 0)

	for li, level := range levels {
		mu.Lock()
		snapshot := make(map[string]map[string]any, len(applied))
		for k, v := range applied {
			snapshot[k] = v
		}
		mu.Unlock()

		sem := make(chan struct{}, opts.parallelism())
		var wg sync.WaitGroup
		errs := make([]error, len(level))

		for i, name := range level {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, name string) {
				defer wg.Done()
				defer func() { <-sem }()

				var out = e.Stdout
				var buf *bytes.Buffer
				if buffered {
					buf = &bytes.Buffer{}
					out = buf
				}

				e.logger().Debug("running node", "node", name)
				outputs, status, err := action(name, snapshot, out)

				if buf != nil {
					outMu.Lock()
					_, _ = fmt.Fprintf(e.Stdout, "=== node %s ===\n", name)
					_, _ = io.Copy(e.Stdout, buf)
					outMu.Unlock()
				}

				mu.Lock()
				if err != nil {
					runs = append(runs, NodeRun{Node: name, Level: li + 1, Status: StatusFailed, Err: err})
				} else {
					runs = append(runs, NodeRun{Node: name, Level: li + 1, Status: status})
					if outputs != nil {
						applied[name] = outputs
					}
				}
				mu.Unlock()

				if err != nil {
					errs[i] = fmt.Errorf("node %q: %w", name, err)
					return
				}
			}(i, name)
		}
		wg.Wait()

		for _, err := range errs {
			if err != nil {
				return markNotRun(runs, levels, li+1), err
			}
		}

		if afterLevel != nil {
			if err := afterLevel(); err != nil {
				return markNotRun(runs, levels, li+1), err
			}
		}
	}
	return runs, nil
}

// markNotRun appends a StatusNotRun entry for every node in the levels an aborted run never reached, keeping each entry's Level aligned with the numbering the completed levels already used.
func markNotRun(runs []NodeRun, levels [][]string, from int) []NodeRun {
	for i := from; i < len(levels); i++ {
		for _, name := range levels[i] {
			runs = append(runs, NodeRun{Node: name, Level: i + 1, Status: StatusNotRun})
		}
	}
	return runs
}
