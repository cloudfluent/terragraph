package engine

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// Plan runs `terraform plan` over the selected nodes in topological order, returning each node's outcome (see NodeRun). A node downstream of one that has never been applied will fail to resolve its inputs. See the "known limitation" in the project plan: planning a value that doesn't exist yet is inherently impossible when every node is an independent root module.
func (e *Engine) Plan(opts Options) ([]NodeRun, error) {
	return e.plan(opts, false, false)
}

// ReviewPlan inspects ephemeral saved plans while preserving independent results after failed dependencies.
func (e *Engine) ReviewPlan(opts Options, allowTextFallback bool) ([]NodeRun, error) {
	return e.plan(opts, true, allowTextFallback)
}

func (e *Engine) plan(opts Options, inspect, allowTextFallback bool) (runs []NodeRun, resultErr error) {
	unlock, err := e.lockRun()
	if err != nil {
		return nil, err
	}
	defer unlock()

	if !inspect {
		if err := e.checkRuntimeFiles(opts); err != nil {
			return nil, err
		}
	}

	unlockGraph, err := e.lockGraph()
	if err != nil {
		return nil, err
	}
	defer unlockGraph()

	session, err := e.startExecution("plan", opts, false)
	if err != nil {
		return nil, err
	}
	defer session.close()
	defer func() {
		if finishErr := session.finish(resultErr); finishErr != nil {
			resultErr = errors.Join(resultErr, finishErr)
		}
	}()

	e.logger().Info("plan starting", "node", opts.Node, "parallelism", opts.parallelism())
	var reviewMu sync.Mutex
	reviews := map[string]*PlanReview{}
	runs, runErr := e.runLevels(opts, false, func(name string, applied map[string]exec.Outputs, out io.Writer) (exec.Outputs, string, error) {
		review := newPlanReview(e.approveFor(name, opts.Approve))
		if inspect {
			reviewMu.Lock()
			reviews[name] = review
			reviewMu.Unlock()
		}
		fail := func(code, phase string, err error) (exec.Outputs, string, error) {
			if inspect {
				review.failure(name, code, phase, err)
			}
			return nil, "", err
		}
		if inspect {
			if err := e.checkRuntimeFiles(Options{Node: name}); err != nil {
				return fail("runtime_incompatible", "prepare", err)
			}
		}
		var basis *[]InputBasis
		if inspect {
			basis = &review.Inputs
		}
		vars, err := e.resolveInputsWithBasis(name, applied, basis)
		if err != nil {
			return fail("input_resolution_failed", "inputs", err)
		}
		nodeDir := e.nodeDir(name)
		varsPath := e.tfVarsPath(name)
		if _, err := exec.WriteTFVars(varsPath, vars); err != nil {
			return fail("variables_write_failed", "prepare", err)
		}
		// Removed however this node exits: the file holds resolved input values in cleartext, and the next run rewrites it from scratch anyway.
		defer func() { _ = os.Remove(varsPath) }()

		r := &exec.Runner{Context: e.context(), Binary: e.runtimeFor(name), Dir: nodeDir, DataDir: e.dataDir(name), Env: e.envFor(name), Stdout: out, Stderr: out}
		if err := session.transition(name, "initializing", "", ""); err != nil {
			return fail("journal_failed", "init", err)
		}
		if err := r.Init(e.Graph.Nodes[name].BackendConfig); err != nil {
			return fail("initialization_failed", "init", session.fail(name, "indeterminate", fmt.Errorf("init: %w", err)))
		}

		if err := session.transition(name, "preparing", "", ""); err != nil {
			return fail("journal_failed", "init", err)
		}

		if inspect {
			backend := e.Graph.Nodes[name].Schema.Backend
			if backend == "remote" || backend == "cloud" || !r.SupportsSavedPlan() {
				capability := fmt.Errorf("backend does not support saved-plan inspection; use text plan for native preview")
				review.failure(name, "inspection_unsupported", "capability", capability)
				if !allowTextFallback {
					return nil, "", capability
				}
			} else {
				planPath := e.planPath(name)
				cleanup, err := prepareSavedPlan(planPath)
				if err != nil {
					return fail("plan_artifact_failed", "prepare", err)
				}
				defer cleanup()
				changed, err := r.PlanChanges(planPath, exec.VarFileArgs(varsPath, vars)...)
				if err != nil {
					return fail("plan_failed", "plan", fmt.Errorf("plan: %w", err))
				}
				review.Resources, err = r.PlanChangeSet(planPath, &review.Outputs)
				if err != nil {
					return fail("inspection_failed", "inspect", err)
				}
				review.normalize(changed)
				if err := session.transition(name, "completed", "", ""); err != nil {
					return fail("journal_failed", "plan", err)
				}
				return nil, StatusPlanned, nil
			}
		}
		if err := r.Plan(exec.VarFileArgs(varsPath, vars)...); err != nil {
			return fail("plan_failed", "plan", fmt.Errorf("plan: %w", err))
		}
		if err := session.transition(name, "completed", "", ""); err != nil {
			return fail("journal_failed", "plan", err)
		}
		return nil, StatusPlanned, nil
	}, nil, inspect)
	if inspect {
		for i := range runs {
			review := reviews[runs[i].Node]
			if review == nil {
				review = newPlanReview(e.approveFor(runs[i].Node, opts.Approve))
				reason := runs[i].Err
				if reason == nil {
					reason = fmt.Errorf("plan was not reached; retry after resolving earlier diagnostics")
				}
				review.failure(runs[i].Node, "not_reached", "schedule", reason)
			}
			runs[i].Review = review
		}
	}
	return runs, runErr
}
