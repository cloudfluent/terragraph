package engine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
)

// Options controls the scope and execution behavior of a plan/apply/destroy run.
type Options struct {
	// RetainPlan persists optional plan artifacts without pausing ordinary apply.
	RetainPlan bool
	// Nodes is nil for the whole graph; a non-nil empty list must never silently broaden execution.
	Nodes []string
	// Downstream follows both data and ordering dependencies so consumers cannot be omitted by edge kind.
	Downstream bool
	// SelectionSpecified preserves explicit false flags so stored executions reject every scope override.
	SelectionSpecified bool
	// OnSelection publishes resolved scope before runtime subprocesses, keeping presentation out of the engine.
	OnSelection func(*graph.Selection, [][]string)
	selection   *graph.Selection
	levels      [][]string
	// AutoApprove skips the interactive approval Apply would otherwise ask for, and is forwarded to `terraform destroy` as -auto-approve. It governs only whether a human is asked; what a node is permitted to do unattended is Approve's job, and the two are checked independently.
	AutoApprove bool
	// Approve is the run-wide default approve level (see blueprint.Approve) for nodes that declare none of their own. Empty means blueprint.ApproveSafe.
	Approve blueprint.Approve
	// Parallelism caps how many ready nodes run concurrently across execution levels. <=1 means sequential (the default), matching v1 behavior and avoiding surprising provider API rate-limit issues.
	Parallelism int
}

func (o Options) parallelism() int {
	if o.Parallelism < 1 {
		return 1
	}
	return o.Parallelism
}

func (e *Engine) executionLevels(opts Options, reverse bool) ([][]string, error) {
	if opts.levels == nil {
		var err error
		opts, err = e.resolveSelection(opts)
		if err != nil {
			return nil, err
		}
	}
	levels := append([][]string{}, opts.levels...)
	if reverse {
		slices.Reverse(levels)
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
	StatusNotRun    = "not run"   // execution stopped or a prerequisite failed before this node started
)

// RunResult retains the persisted execution identity even when a later node or journal update fails.
type RunResult struct {
	ExecutionID string
	Nodes       []NodeRun
}

// NodeRun records one node's outcome in a plan/apply/destroy run; reports are sorted by Level, then Node, regardless of concurrent completion order. Level is 1-based in execution order (reversed for destroy), so a caller can present results in run order without re-deriving the graph; Err is the node's own error, without the node %q prefix runLevels adds when failing the run.
type NodeRun struct {
	Node        string
	Level       int
	Status      string
	Err         error
	Diagnostics []Diagnostic
	// Review is present only for explicit plan inspection and never acts as apply authorization.
	Review *PlanReview
}

// nodeAction runs one node's step of a plan/apply/destroy: given the outputs applied so far this run and a writer for this node's terraform output, it returns the outputs to feed downstream (nil if the node produced none worth propagating, e.g. Destroy), the success status to record for the node, and an error.
type nodeAction func(name string, applied map[string]exec.Outputs, out io.Writer) (outputs exec.Outputs, status string, err error)

// runLevels dispatches selected nodes as their prerequisites succeed, retaining level labels and the existing failure boundary so queued siblings still run after an ordinary failure.
func (e *Engine) runLevels(opts Options, reverse bool, action nodeAction, afterLevel func() error, preserveIndependent ...bool) (runs []NodeRun, err error) {
	levels, err := e.executionLevels(opts, reverse)
	if err != nil {
		return nil, err
	}
	keepGoing := len(preserveIndependent) > 0 && preserveIndependent[0]
	runs = make([]NodeRun, 0)
	indices := map[string]int{}
	for li, level := range levels {
		for _, name := range level {
			indices[name] = len(runs)
			runs = append(runs, NodeRun{Node: name, Level: li + 1, Status: StatusNotRun})
		}
	}
	// Only the coordinator mutates reports and published outputs; each action receives its own immutable snapshot.
	started, done := make([]bool, len(runs)), make([]bool, len(runs))
	applied := map[string]exec.Outputs{}
	type completion struct {
		index   int
		outputs exec.Outputs
		status  string
		err     error
		buffer  *bytes.Buffer
	}
	completed := make(chan completion, opts.parallelism())
	active, failureLevel, nextHook := 0, len(levels)+1, 1
	var stopErr error
	finish := func(i int, status string, nodeErr error) {
		done[i] = true
		runs[i].Status, runs[i].Err = status, nodeErr
		if nodeErr != nil {
			runs[i].Diagnostics = Diagnostics(nodeErr, Diagnostic{Code: "runtime_failed", Category: "runtime", Phase: "execute", Subject: "node." + runs[i].Node})
			if !keepGoing && runs[i].Level < failureLevel {
				failureLevel = runs[i].Level
			}
		}
	}
	for {
		if cancelled := e.context().Err(); cancelled != nil {
			stopErr = cancelled
		}
		// A caller-supplied level checkpoint must succeed before work beyond that checkpoint can start.
		if afterLevel != nil && stopErr == nil {
			for nextHook <= len(levels) && (keepGoing || nextHook < failureLevel) {
				complete := true
				for _, name := range levels[nextHook-1] {
					complete = complete && done[indices[name]]
				}
				if !complete {
					break
				}
				if hookErr := afterLevel(); hookErr != nil {
					stopErr = hookErr
					break
				}
				nextHook++
			}
		}
		for i := range runs {
			if active == opts.parallelism() || stopErr != nil {
				break
			}
			if started[i] || done[i] || runs[i].Level > failureLevel || (afterLevel != nil && runs[i].Level > nextHook) {
				continue
			}
			parents := e.Graph.In[runs[i].Node]
			if reverse {
				parents = e.Graph.Out[runs[i].Node]
			}
			ready, blocked := true, ""
			for _, parent := range parents {
				pi, selected := indices[parent]
				if !selected {
					continue
				}
				if !done[pi] {
					ready = false
				}
				if done[pi] && runs[pi].Err != nil && (blocked == "" || parent < blocked) {
					blocked = parent
				}
			}
			if blocked != "" {
				if keepGoing {
					finish(i, StatusNotRun, fmt.Errorf("%w: dependency %s has no successful plan evidence; resolve its diagnostic first", errPlanBlocked, blocked))
				}
				continue
			}
			if !ready {
				continue
			}
			snapshot := make(map[string]exec.Outputs, len(applied))
			for name, outputs := range applied {
				snapshot[name] = outputs
			}
			started[i], active = true, active+1
			go func(i int) {
				result := completion{index: i}
				out := e.Stdout
				if opts.parallelism() > 1 {
					result.buffer = &bytes.Buffer{}
					out = result.buffer
				}
				e.logger().Debug("running node", "node", runs[i].Node)
				if result.err = e.context().Err(); result.err != nil {
					result.status = StatusNotRun
				} else {
					result.outputs, result.status, result.err = action(runs[i].Node, snapshot, out)
					if result.err != nil {
						result.status = StatusFailed
					}
				}
				completed <- result
			}(i)
		}
		if active == 0 {
			break
		}
		result := <-completed
		active--
		if result.buffer != nil {
			_, _ = fmt.Fprintf(e.Stdout, "=== node %s ===\n", runs[result.index].Node)
			_, _ = io.Copy(e.Stdout, result.buffer)
		}
		finish(result.index, result.status, result.err)
		if result.err == nil && result.outputs != nil {
			applied[runs[result.index].Node] = result.outputs
		}
	}
	if stopErr != nil {
		return runs, stopErr
	}
	for _, run := range runs {
		if run.Err != nil {
			return runs, fmt.Errorf("node %q: %w", run.Node, run.Err)
		}
	}
	return runs, nil
}

var errPlanBlocked = errors.New("plan not reached")

// resolveSelection freezes membership before runtime checks and keeps every scheduler on the same filtered levels.
func (e *Engine) resolveSelection(opts Options) (Options, error) {
	selection, err := graph.Select(e.Graph, opts.Nodes, opts.Downstream)
	if err != nil {
		return opts, WithDiagnostic(err, Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "selection", Subject: "selection", Remedy: "select expanded node names from graph output"})
	}
	var names []string
	if selection != nil {
		names = selection.Names()
	}
	levels, err := graph.SelectedLevels(e.Graph, names, false)
	if err != nil {
		return opts, err
	}
	opts.selection, opts.levels = selection, levels
	return opts, nil
}

func (o Options) hasSelectionFlags() bool {
	return o.SelectionSpecified || o.Nodes != nil || o.Downstream
}

func (o Options) announceSelection(reverse bool) {
	if o.OnSelection == nil {
		return
	}
	levels := append([][]string{}, o.levels...)
	if reverse {
		slices.Reverse(levels)
	}
	o.OnSelection(o.selection, levels)
}
