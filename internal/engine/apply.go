package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// Apply runs `terraform apply` over the selected nodes in topological order, feeding each node's real outputs into whatever downstream nodes are applied later in the same run.
//
// Whether a node needs applying is Terraform's decision, not terragraph's: every node is planned with -refresh=true and -detailed-exitcode, and a plan reporting no changes skips the apply. Nothing local is consulted first. An earlier version of this kept a content-addressed cache of source files, resolved inputs and execution identity as a prefilter, which was wrong in three separate ways (backend and inherited context missing from the key, drift never refreshed, files read through file()/templatefile() never invalidating) and, once every hit had to be confirmed by a plan anyway, only served to send *misses* straight to apply without one.
//
// When the plan does report changes, that plan is what gets applied (see Runner.PlanChanges/ApplyPlan), so a node refreshes once and the change made is the change that was planned.
func (e *Engine) Apply(opts Options) error {
	// Concurrent nodes have their output buffered and flushed a node at a time (see runLevels), so a prompt written mid-level would be invisible until long after the answer was needed. Rather than deadlock on that, say so — before taking the run lock, so a combination that cannot run fails immediately instead of first waiting on whatever else holds it.
	if !opts.AutoApprove && opts.parallelism() > 1 {
		return fmt.Errorf("--parallelism %d needs --auto-approve: output from concurrent nodes is buffered, so there is nowhere to ask for approval", opts.parallelism())
	}

	unlock, err := e.lockRun()
	if err != nil {
		return err
	}
	defer unlock()

	unlockGraph, err := e.lockGraph()
	if err != nil {
		return err
	}
	defer unlockGraph()

	e.logger().Info("apply starting", "node", opts.Node, "parallelism", opts.parallelism(), "autoApprove", opts.AutoApprove)

	return e.runLevels(opts, false, func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, error) {
		vars, err := e.resolveInputs(name, applied)
		if err != nil {
			return nil, err
		}

		varsPath := e.tfVarsPath(name)
		if _, err := exec.WriteTFVars(varsPath, vars); err != nil {
			return nil, err
		}
		varFileArgs := exec.VarFileArgs(varsPath, vars)

		r := &exec.Runner{Binary: e.runtimeFor(name), Dir: e.nodeDir(name), DataDir: e.dataDir(name), Env: e.envFor(name), Stdout: out, Stderr: out}
		if err := r.Init(e.Graph.Nodes[name].BackendConfig); err != nil {
			return nil, fmt.Errorf("init: %w", err)
		}

		// remote/cloud run the plan on HCP and cannot write a local plan file. Applying without one would skip the approve gate, so this path is refused until that backend can be inspected the same way. State-storage backends (s3, gcs, ...) are unaffected.
		if !r.SupportsSavedPlan() {
			e.logger().Warn("apply refused: backend cannot produce a local plan", "node", name, "backend", r.BackendType())
			return nil, savedPlanUnsupportedError(name, r.BackendType())
		}

		savedPlan := e.planPath(name)
		if err := os.MkdirAll(filepath.Dir(savedPlan), 0o755); err != nil {
			return nil, fmt.Errorf("creating plan directory: %w", err)
		}
		// Removed however this node exits: the file holds resolved input values in cleartext, and a plan left behind is only ever stale by the next run.
		defer func() { _ = os.Remove(savedPlan) }()

		changes, err := r.PlanChanges(savedPlan, varFileArgs...)
		if err != nil {
			return nil, fmt.Errorf("plan: %w", err)
		}
		if !changes {
			e.logger().Debug("plan reports no changes, skipping apply", "node", name)
			_, _ = fmt.Fprintf(out, "node %s: unchanged, skipping apply\n", name)
			outputs, err := r.Outputs()
			if err != nil {
				return nil, fmt.Errorf("plan says unchanged but outputs are unreadable: %w", err)
			}
			return outputs, nil
		}

		// What the plan actually does, read back from the file before any of it happens. Local only: no state is refreshed and no provider is called.
		changeSet, err := r.PlanChangeSet(savedPlan)
		if err != nil {
			return nil, fmt.Errorf("reading plan: %w", err)
		}
		_, _ = fmt.Fprintf(out, "node %s: %s\n", name, summarizeChanges(changeSet))

		// Levels run in order, so refusing here means nothing downstream runs either: the cascade is cut at the node that caused it rather than audited after the fact.
		level := e.approveFor(name, opts.Approve)
		if blocked := notPermitted(changeSet, level); len(blocked) > 0 {
			return nil, gateError(name, level, blocked)
		}

		// The plan Terraform just printed is the plan about to be applied, so this asks about something the user has actually seen — which is the whole reason approval belongs here rather than inside a second `apply` that would plan again from scratch.
		if !opts.AutoApprove {
			approved, err := e.approve(name, out)
			if err != nil {
				return nil, err
			}
			if !approved {
				return nil, fmt.Errorf("apply cancelled: node %s was not approved", name)
			}
		}
		if err := r.ApplyPlan(savedPlan); err != nil {
			return nil, fmt.Errorf("apply: %w", err)
		}

		outputs, err := r.Outputs()
		if err != nil {
			return nil, fmt.Errorf("reading outputs after apply: %w", err)
		}
		return outputs, nil
	}, nil)
}

func savedPlanUnsupportedError(name, backend string) error {
	return fmt.Errorf("node %s uses the %q backend, which cannot produce a local plan; terragraph apply needs one to decide what may be applied. Use a state-storage backend (s3, gcs, azurerm, http, local) instead", name, backend)
}
