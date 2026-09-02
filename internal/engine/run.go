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

// nodeAction runs one node's step of a plan/apply/destroy: given the outputs applied so far this run and a writer for this node's terraform output, it returns the outputs to feed downstream (nil if the node produced none worth propagating, e.g. Destroy).
type nodeAction func(name string, applied map[string]map[string]any, out io.Writer) (outputs map[string]any, err error)

// runLevels is the shared execution loop behind Plan/Apply/Destroy: it walks the graph (or a single node) level by level, running up to opts.Parallelism nodes within a level concurrently. Nodes in the same level are guaranteed to have no edge between them, so a read-only snapshot of outputs applied so far is safe to share across the level's goroutines, and results are merged back only once the whole level completes (no data races). If any node in a level errors, already-started siblings finish but the next level never starts. afterLevel, if non-nil, runs once each level completes successfully (used by Apply to persist the incremental-apply cache); an error from it aborts the run.
func (e *Engine) runLevels(opts Options, reverse bool, action nodeAction, afterLevel func() error) error {
	levels, err := e.executionLevels(opts, reverse)
	if err != nil {
		return err
	}

	applied := map[string]map[string]any{}
	var mu sync.Mutex
	var outMu sync.Mutex
	buffered := opts.parallelism() > 1

	for _, level := range levels {
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
				outputs, err := action(name, snapshot, out)

				if buf != nil {
					outMu.Lock()
					_, _ = fmt.Fprintf(e.Stdout, "=== node %s ===\n", name)
					_, _ = io.Copy(e.Stdout, buf)
					outMu.Unlock()
				}

				if err != nil {
					errs[i] = fmt.Errorf("node %q: %w", name, err)
					return
				}
				if outputs != nil {
					mu.Lock()
					applied[name] = outputs
					mu.Unlock()
				}
			}(i, name)
		}
		wg.Wait()

		for _, err := range errs {
			if err != nil {
				return err
			}
		}

		if afterLevel != nil {
			if err := afterLevel(); err != nil {
				return err
			}
		}
	}
	return nil
}
