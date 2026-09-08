package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// Options controls the scope and execution behavior of a plan/apply/destroy run.
type Options struct {
	// Node restricts the operation to a single node. Empty means the whole graph, in topological (or, for Destroy, reverse topological) order.
	Node string
	// Nodes extends the legacy single-node API with a union of explicit leaf targets.
	Nodes               []string
	IncludeDependencies bool
	IncludeDependents   bool
	// AllowOrphanDestroy acknowledges consumers outside a partial destroy without changing their selection.
	AllowOrphanDestroy bool
	KeepGoing          bool
	FailFast           bool
	// NodeTimeout bounds the entire node action, including input reads and approval, after it receives a scheduler slot.
	NodeTimeout   time.Duration
	Timeouts      map[string]time.Duration
	OutputRetries int
	Pools         []ConcurrencyPool
	// RecordRun persists only execution metadata; Resume selects unfinished work and never authorizes cached plans.
	RecordRun bool
	Resume    bool
	operation string
	// AutoApprove skips the interactive approval Apply would otherwise ask for, and is forwarded to `terraform destroy` as -auto-approve. It governs only whether a human is asked; what a node is permitted to do unattended is Approve's job, and the two are checked independently.
	AutoApprove bool
	// Approve is the run-wide default approve level (see blueprint.Approve) for nodes that declare none of their own. Empty means blueprint.ApproveSafe.
	Approve blueprint.Approve
	// Parallelism caps ready nodes across the selected DAG. <=1 means sequential (the default), matching v1 behavior and avoiding surprising provider API rate-limit issues.
	Parallelism int
}

func (o Options) parallelism() int {
	if o.Parallelism < 1 {
		return 1
	}
	return o.Parallelism
}

func (e *Engine) executionLevels(opts Options, reverse bool) ([][]string, error) {
	selection, err := e.selection(opts)
	if err != nil {
		return nil, err
	}
	levels := selection.Levels
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
	StatusNotRun    = "not run"   // dependencies, failure policy, or cancellation prevented dispatch
)

// NodeRun records one node's outcome in a plan/apply/destroy run; reports are sorted by Level, then Node, regardless of concurrent completion order. Level is 1-based in execution order (reversed for destroy), so a caller can present results in run order without re-deriving the graph; Err is the node's own error, without the node %q prefix runLevels adds when failing the run.
type NodeRun struct {
	Node   string
	Level  int
	Status string
	Err    error
	// Review is present only for explicit plan inspection and never acts as apply authorization.
	Review    *PlanReview
	Reason    string
	BlockedBy []string
	StartedAt time.Time
	Duration  time.Duration
}

// nodeAction runs one node's step of a plan/apply/destroy: given the outputs applied so far this run and a writer for this node's terraform output, it returns the outputs to feed downstream (nil if the node produced none worth propagating, e.g. Destroy), the success status to record for the node, and an error.
type nodeAction func(ctx context.Context, name string, applied map[string]exec.Outputs, out io.Writer) (outputs exec.Outputs, status string, err error)

// runLevels dispatches a node once its selected prerequisites succeed; a single coordinator owns reports, outputs, and pool slots to prevent cross-level races.
func (e *Engine) runLevels(opts Options, reverse bool, action nodeAction, afterLevel func() error, preserveIndependent ...bool) ([]NodeRun, error) {
	if err := e.validateOptions(opts); err != nil {
		return nil, err
	}
	levels, err := e.executionLevels(opts, reverse)
	if err != nil {
		return nil, err
	}
	keepGoing := opts.KeepGoing || (len(preserveIndependent) > 0 && preserveIndependent[0])
	prerequisites := e.Graph.In
	if reverse {
		prerequisites = e.Graph.Out
	}
	runs := []NodeRun{}
	indices := map[string]int{}
	for li, level := range levels {
		for _, name := range level {
			indices[name] = len(runs)
			runs = append(runs, NodeRun{Node: name, Level: li + 1, Status: StatusNotRun, Reason: "pending"})
		}
	}
	state := &executionState{runs: runs, indices: indices, finished: map[string]bool{}, active: map[string]bool{}, applied: map[string]exec.Outputs{}, poolUse: map[string]int{}, pools: opts.Pools}
	if opts.RecordRun {
		if err := e.writeRunRecord(opts.operation, runs); err != nil {
			for i := range runs {
				runs[i].Reason = "history_failed"
			}
			return runs, err
		}
	}
	completed := make(chan nodeCompletion, opts.parallelism())
	var stopErr error
	barrier := 1
	for len(state.finished) < len(runs) {
		if err := e.context().Err(); err != nil {
			stopErr = err
		}
		state.blockDependents(prerequisites)
		if stopErr == nil {
			for i := range runs {
				if len(state.active) >= opts.parallelism() {
					break
				}
				run := &runs[i]
				if state.active[run.Node] || state.finished[run.Node] || (afterLevel != nil && run.Level > barrier) {
					continue
				}
				if !state.ready(run.Node, prerequisites) || !state.poolAvailable(run.Node) {
					continue
				}
				// Persist running before dispatch so an interrupted action is retried instead of being mistaken for a successful node.
				run.Status, run.Reason = StatusRunning, ""
				run.StartedAt = time.Now().UTC()
				if opts.RecordRun {
					if err := e.writeRunRecord(opts.operation, runs); err != nil {
						run.Status, run.Reason, run.StartedAt = StatusNotRun, "history_failed", time.Time{}
						stopErr = err
						break
					}
				}
				state.active[run.Node] = true
				state.usePools(run.Node, 1)
				applied := make(map[string]exec.Outputs, len(state.applied))
				for name, outputs := range state.applied {
					applied[name] = outputs
				}
				go e.executeNode(opts, *run, applied, action, completed)
			}
		}
		if len(state.active) == 0 {
			break
		}
		result := <-completed
		i := indices[result.run.Node]
		runs[i] = result.run
		state.finished[result.run.Node] = true
		delete(state.active, result.run.Node)
		state.usePools(result.run.Node, -1)
		if result.run.Err == nil && result.outputs != nil {
			state.applied[result.run.Node] = result.outputs
		}
		if result.buffer != nil {
			_, _ = fmt.Fprintf(e.Stdout, "=== node %s ===\n", result.run.Node)
			_, _ = io.Copy(e.Stdout, result.buffer)
		}
		if result.run.Err != nil && !keepGoing && stopErr == nil {
			stopErr = result.run.Err
		}
		if opts.RecordRun {
			if err := e.writeRunRecord(opts.operation, runs); err != nil {
				stopErr = errors.Join(stopErr, err)
			}
		}
		if afterLevel != nil && state.levelFinished(barrier) {
			if err := afterLevel(); err != nil {
				stopErr = errors.Join(stopErr, err)
			}
			barrier++
		}
	}
	state.blockDependents(prerequisites)
	for i := range runs {
		if state.finished[runs[i].Node] {
			continue
		}
		runs[i].Reason = "fail_fast"
		if e.context().Err() != nil {
			runs[i].Reason = "cancelled"
			runs[i].Err = e.context().Err()
		}
	}
	var failures []error
	for _, run := range runs {
		if run.Err != nil {
			failures = append(failures, fmt.Errorf("node %q: %w", run.Node, run.Err))
		}
	}
	if stopErr != nil {
		represented := false
		for _, run := range runs {
			if errors.Is(run.Err, stopErr) {
				represented = true
				break
			}
		}
		if !represented {
			failures = append(failures, stopErr)
		}
	}
	if opts.RecordRun {
		if err := e.writeRunRecord(opts.operation, runs); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) == 1 {
		return runs, failures[0]
	}
	return runs, errors.Join(failures...)
}

var errPlanBlocked = errors.New("dependency not reached")

const StatusRunning = "running"

type nodeCompletion struct {
	run     NodeRun
	outputs exec.Outputs
	buffer  *bytes.Buffer
}

// executeNode keeps timeout cancellation inside one action and waits for its cleanup before returning its pool slots.
func (e *Engine) executeNode(opts Options, run NodeRun, applied map[string]exec.Outputs, action nodeAction, completed chan<- nodeCompletion) {
	ctx := e.context()
	timeout := opts.NodeTimeout
	if value, ok := opts.Timeouts[run.Node]; ok {
		timeout = value
	}
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	out := e.Stdout
	var buffer *bytes.Buffer
	if opts.parallelism() > 1 {
		buffer = &bytes.Buffer{}
		out = buffer
	}
	var outputs exec.Outputs
	status, err := StatusNotRun, ctx.Err()
	if err == nil {
		outputs, status, err = action(ctx, run.Node, applied, out)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	run.Status, run.Err, run.Duration = status, err, time.Since(run.StartedAt)
	if err != nil {
		run.Status, run.Reason = StatusFailed, "action_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			run.Reason = "timeout"
		}
		if errors.Is(err, context.Canceled) {
			run.Reason = "cancelled"
		}
	}
	completed <- nodeCompletion{run: run, outputs: outputs, buffer: buffer}
}

// executionState is coordinator-owned so no mutex or partial pool acquisition can block another ready branch.
type executionState struct {
	runs             []NodeRun
	indices          map[string]int
	finished, active map[string]bool
	applied          map[string]exec.Outputs
	pools            []ConcurrencyPool
	poolUse          map[string]int
}

func (s *executionState) ready(name string, prerequisites map[string][]string) bool {
	for _, parent := range prerequisites[name] {
		if _, selected := s.indices[parent]; selected && !s.finished[parent] {
			return false
		}
	}
	return true
}

func (s *executionState) blockDependents(prerequisites map[string][]string) {
	for i := range s.runs {
		run := &s.runs[i]
		if (s.finished[run.Node] && run.Reason != "dependency_failed") || s.active[run.Node] {
			continue
		}
		blocked := map[string]bool{}
		for _, parent := range prerequisites[run.Node] {
			j, selected := s.indices[parent]
			if !selected || !s.finished[parent] {
				continue
			}
			upstream := s.runs[j]
			if upstream.Err == nil && upstream.Status != StatusNotRun {
				continue
			}
			if len(upstream.BlockedBy) > 0 {
				for _, name := range upstream.BlockedBy {
					blocked[name] = true
				}
			} else {
				blocked[parent] = true
			}
		}
		if len(blocked) == 0 {
			continue
		}
		run.BlockedBy = nil
		for name := range blocked {
			run.BlockedBy = append(run.BlockedBy, name)
		}
		sort.Strings(run.BlockedBy)
		run.Reason = "dependency_failed"
		run.Err = fmt.Errorf("%w: %s; resolve upstream diagnostics and rerun", errPlanBlocked, strings.Join(run.BlockedBy, ", "))
		s.finished[run.Node] = true
	}
}

func (s *executionState) poolAvailable(name string) bool {
	for _, pool := range s.pools {
		if slices.Contains(pool.Nodes, name) && s.poolUse[pool.Name] >= pool.Limit {
			return false
		}
	}
	return true
}
func (s *executionState) usePools(name string, delta int) {
	for _, pool := range s.pools {
		if slices.Contains(pool.Nodes, name) {
			s.poolUse[pool.Name] += delta
		}
	}
}
func (s *executionState) levelFinished(level int) bool {
	for _, run := range s.runs {
		if run.Level == level && !s.finished[run.Node] {
			return false
		}
	}
	return true
}
