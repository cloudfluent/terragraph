package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
	sdk "github.com/cloudfluent/terragraph/plugin"
)

// Options controls the scope and execution behavior of a plan/apply/destroy run.
type Options struct {
	// RetainPlan persists optional plan artifacts without pausing ordinary apply.
	RetainPlan bool
	// Pools constrain shared services without introducing artificial dependency edges.
	Pools []ConcurrencyPool
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
	if err := e.validatePools(opts); err != nil {
		return nil, err
	}
	levels, err := e.executionLevels(opts, reverse)
	if err != nil {
		return nil, err
	}
	keepGoing := len(preserveIndependent) > 0 && preserveIndependent[0]
	runs = make([]NodeRun, 0)
	indices := map[string]int{}
	poolUse := make([]int, len(opts.Pools))
	poolFor := map[string][]int{}
	for i, pool := range opts.Pools {
		for _, name := range pool.Nodes {
			poolFor[name] = append(poolFor[name], i)
		}
	}
	for li, level := range levels {
		for _, name := range level {
			indices[name] = len(runs)
			runs = append(runs, NodeRun{Node: name, Level: li + 1, Status: StatusNotRun})
		}
	}
	// Nodes a failure, gate, or cancellation kept from starting still owe observers a terminal event; the lifecycle drops repeats for nodes that already reported one.
	defer func() {
		if e.plugins == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(e.context()), 5*time.Second)
		defer cancel()
		for _, run := range runs {
			if emitErr := e.plugins.Emit(ctx, sdk.Event{Phase: "node.finished", Node: run.Node, Status: run.Status}); emitErr != nil {
				e.plugins.AddNodeCompletionError(run.Node, emitErr)
			}
		}
	}()
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
	active, failureLevel, nextLevel, unattributedLevel := 0, len(levels)+1, 1, len(levels)+1
	// A level.finished gate may deny work beyond that level, so its decision must precede any later dispatch; observers and plugin-free runs keep levels overlapping.
	gated := afterLevel != nil || e.plugins.Has("level.finished")
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
	// A postcondition failure stops work beyond its node's level and withholds that level's notification, while earlier completed levels still report; a failure with no node is pinned to the level whose completion revealed it.
	completionLevel := func() int {
		level := len(levels) + 1
		nodes, unattributed := e.plugins.CompletionFailures()
		if unattributed {
			level = unattributedLevel
		}
		for _, node := range nodes {
			if i, selected := indices[node]; selected && runs[i].Level < level {
				level = runs[i].Level
			}
		}
		return level
	}
	levelDone := func(level int) bool {
		for _, name := range levels[level-1] {
			if !done[indices[name]] {
				return false
			}
		}
		return true
	}
	for {
		if cancelled := e.context().Err(); cancelled != nil && stopErr == nil {
			stopErr = cancelled
		}
		// level.finished follows every node.finished of its level and keeps level order even when an overlapping later level completed first; a failed level still reports, levels beyond the boundary never do.
		for stopErr == nil && nextLevel <= len(levels) && nextLevel <= failureLevel && nextLevel < completionLevel() && levelDone(nextLevel) {
			if emitErr := e.plugins.Emit(e.context(), sdk.Event{Phase: "level.finished", Nodes: levels[nextLevel-1]}); emitErr != nil {
				stopErr = emitErr
				break
			}
			if afterLevel != nil && nextLevel < failureLevel {
				if hookErr := afterLevel(); hookErr != nil {
					stopErr = hookErr
					break
				}
			}
			nextLevel++
		}
		closed := false
		for i := range runs {
			if active == opts.parallelism() || stopErr != nil {
				break
			}
			// The boundary is re-read per candidate because closing a blocked node or a running node's delivery can record a failure mid-scan.
			if started[i] || done[i] || runs[i].Level > min(failureLevel, completionLevel()) || (gated && runs[i].Level > nextLevel) {
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
					// Delivered before the node counts as done so its level's notification can never precede it.
					if e.plugins != nil {
						ctx, cancel := context.WithTimeout(context.WithoutCancel(e.context()), 5*time.Second)
						if emitErr := e.plugins.Emit(ctx, sdk.Event{Phase: "node.finished", Node: runs[i].Node, Status: StatusNotRun}); emitErr != nil {
							e.plugins.AddNodeCompletionError(runs[i].Node, emitErr)
						}
						cancel()
					}
					finish(i, StatusNotRun, fmt.Errorf("%w: dependency %s has no successful plan evidence; resolve its diagnostic first", errPlanBlocked, blocked))
					closed = true
				}
				continue
			}
			if !ready {
				continue
			}
			// Slots are taken by the coordinator only once gates and prerequisites allow dispatch and are returned when the completion arrives, so prepare denial, failure, and cancellation all release them and a pool-blocked node never started still gets its deferred terminal event.
			for _, pi := range poolFor[runs[i].Node] {
				if poolUse[pi] >= opts.Pools[pi].Limit {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			for _, pi := range poolFor[runs[i].Node] {
				poolUse[pi]++
			}
			snapshot := make(map[string]exec.Outputs, len(applied))
			for name, outputs := range applied {
				snapshot[name] = outputs
			}
			started[i], active = true, active+1
			go func(i int, name string) {
				result := completion{index: i}
				out := e.Stdout
				if opts.parallelism() > 1 {
					result.buffer = &bytes.Buffer{}
					out = result.buffer
				}
				e.logger().Debug("running node", "node", name)
				result.err = e.context().Err()
				if result.err == nil {
					result.err = e.plugins.Emit(e.context(), sdk.Event{Phase: "node.prepare", Node: name})
				}
				if result.err != nil {
					result.status = StatusNotRun
				} else {
					result.outputs, result.status, result.err = action(name, snapshot, out)
					if result.err != nil && result.status != StatusNotRun {
						result.status = StatusFailed
					}
				}
				// Delivered before the coordinator sees the completion, so level.finished and the completion-error boundary both observe it.
				if e.plugins != nil {
					ctx, cancel := context.WithTimeout(context.WithoutCancel(e.context()), 5*time.Second)
					emitErr := e.plugins.Emit(ctx, sdk.Event{Phase: "node.finished", Node: name, Status: result.status})
					cancel()
					if emitErr != nil {
						e.plugins.AddNodeCompletionError(name, emitErr)
					}
				}
				completed <- result
			}(i, runs[i].Node)
		}
		// Closing a blocked node can complete a level, so its notification and any work it releases get another pass before waiting or returning.
		if closed {
			continue
		}
		if active == 0 {
			break
		}
		result := <-completed
		active--
		for _, pi := range poolFor[runs[result.index].Node] {
			poolUse[pi]--
		}
		if result.buffer != nil {
			_, _ = fmt.Fprintf(e.Stdout, "=== node %s ===\n", runs[result.index].Node)
			_, _ = io.Copy(e.Stdout, result.buffer)
		}
		finish(result.index, result.status, result.err)
		if result.err == nil && result.outputs != nil {
			applied[runs[result.index].Node] = result.outputs
		}
		if _, unattributed := e.plugins.CompletionFailures(); unattributed && runs[result.index].Level < unattributedLevel {
			unattributedLevel = runs[result.index].Level
		}
	}
	if stopErr != nil {
		return runs, stopErr
	}
	if completionErr := e.plugins.CompletionError(); completionErr != nil {
		return runs, completionErr
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
	if err := e.validatePools(opts); err != nil {
		return opts, err
	}
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
