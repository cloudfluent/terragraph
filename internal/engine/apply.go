package engine

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// Apply runs `terraform apply` over the selected nodes in topological order, feeding each node's real outputs into whatever downstream nodes are applied later in the same run.
//
// Whether a node needs applying is Terraform's decision, not terragraph's: every node is planned with -refresh=true and -detailed-exitcode, and a plan reporting no changes skips the apply. Nothing local is consulted first. An earlier version of this kept a content-addressed cache of source files, resolved inputs and execution identity as a prefilter, which was wrong in three separate ways (backend and inherited context missing from the key, drift never refreshed, files read through file()/templatefile() never invalidating) and, once every hit had to be confirmed by a plan anyway, only served to send *misses* straight to apply without one.
//
// When the plan does report changes, that plan is what gets applied (see Runner.PlanChanges/ApplyPlan), so a node refreshes once and the change made is the change that was planned.
func (e *Engine) Apply(opts Options) (result RunResult, resultErr error) {
	// Concurrent nodes have their output buffered and flushed a node at a time (see runLevels), so a prompt written mid-level would be invisible until long after the answer was needed. Rather than deadlock on that, say so — before taking the run lock, so a combination that cannot run fails immediately instead of first waiting on whatever else holds it.
	if !opts.AutoApprove && opts.parallelism() > 1 {
		return result, WithDiagnostic(fmt.Errorf("--parallelism %d needs --auto-approve: output from concurrent nodes is buffered, so there is nowhere to ask for approval", opts.parallelism()), Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "arguments"})
	}

	unlock, err := e.lockRun()
	if err != nil {
		return result, err
	}
	defer unlock()

	if err := e.checkRuntimeFiles(opts); err != nil {
		return result, err
	}

	unlockGraph, err := e.lockGraph()
	if err != nil {
		return result, err
	}
	defer unlockGraph()

	session, err := e.startExecution("apply", opts, false)
	if err != nil {
		return result, err
	}
	result.ExecutionID = session.record.ID
	defer session.close()
	defer func() {
		if finishErr := session.finish(resultErr); finishErr != nil {
			resultErr = errors.Join(resultErr, finishErr)
		}
	}()

	e.logger().Info("apply starting", "node", opts.Node, "parallelism", opts.parallelism(), "autoApprove", opts.AutoApprove)

	result.Nodes, resultErr = e.runLevels(opts, false, func(name string, applied map[string]exec.Outputs, out io.Writer) (exec.Outputs, string, error) {
		vars, err := e.resolveInputs(name, applied)
		if err != nil {
			return nil, "", err
		}

		varsPath := e.tfVarsPath(name)
		if _, err := exec.WriteTFVars(varsPath, vars); err != nil {
			return nil, "", err
		}
		// Removed however this node exits: the file holds resolved input values in cleartext, and the next run rewrites it from scratch anyway.
		defer func() { _ = os.Remove(varsPath) }()
		varFileArgs := exec.VarFileArgs(varsPath, vars)

		r := &exec.Runner{Context: e.context(), Binary: e.runtimeFor(name), Dir: e.nodeDir(name), DataDir: e.dataDir(name), Env: e.envFor(name), Stdout: out, Stderr: out}
		if err := session.transition(name, "initializing", "", ""); err != nil {
			return nil, "", err
		}
		if err := e.initNode(name, r); err != nil {
			return nil, "", session.fail(name, "indeterminate", fmt.Errorf("init: %w", err))
		}

		if err := session.transition(name, "preparing", "", ""); err != nil {
			return nil, "", err
		}

		// remote/cloud run the plan on HCP and cannot write a local plan file. Applying without one would skip the approve gate, so this path is refused until that backend can be inspected the same way. State-storage backends (s3, gcs, ...) are unaffected.
		if !r.SupportsSavedPlan() {
			e.logger().Warn("apply refused: backend cannot produce a local plan", "node", name, "backend", r.BackendType())
			return nil, "", savedPlanUnsupportedError(name, r.BackendType())
		}

		var binding string
		if opts.RetainPlan {
			binding, err = e.planBinding(name, r, vars)
			if err != nil {
				return nil, "", err
			}
		}
		plan, err := e.prepareNodePlan(name, r, varFileArgs...)
		if err != nil {
			return nil, "", err
		}
		defer plan.cleanup()
		plan.session = session
		plan.binding = binding
		if opts.RetainPlan {
			if _, err := e.retainPlan(session, plan, vars); err != nil {
				return nil, "", err
			}
		}
		return e.applyPreparedPlan(plan, opts)
	}, nil)
	return result, resultErr
}

func savedPlanUnsupportedError(name, backend string) error {
	return fmt.Errorf("node %s uses the %q backend, which cannot produce a local plan; terragraph apply needs one to decide what may be applied. Use a state-storage backend (s3, gcs, azurerm, http, local) instead", name, backend)
}
