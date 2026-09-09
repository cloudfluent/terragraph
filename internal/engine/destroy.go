package engine

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// Destroy tears down the selected nodes in reverse topological order (downstream first) so a node is never destroyed while something still depends on its outputs.
//
// Nothing has to be invalidated afterwards. Destroy once had to drop the incremental-apply cache entry for everything it tore down, because a stale "unchanged" hit against infrastructure that no longer exists would have been a correctness bug rather than a missed optimization; a later apply now asks Terraform, which plans against real state and sees the resources are gone.
func (e *Engine) Destroy(opts Options) (result RunResult, resultErr error) {
	if err := opts.validateTimeout(true); err != nil {
		return result, err
	}
	opts, selectionErr := e.resolveSelection(opts)
	if selectionErr != nil {
		return result, selectionErr
	}
	opts.announceSelection(true)

	// Same reason Apply refuses it: concurrent nodes have their output buffered and flushed a node at a time, so terraform's confirmation prompt would be invisible until long after the answer was needed. Checked before taking the run lock, so an unrunnable combination fails immediately instead of after waiting for whatever else holds it.
	if !opts.AutoApprove && opts.parallelism() > 1 {
		return result, WithDiagnostic(fmt.Errorf("--parallelism %d needs --auto-approve: output from concurrent nodes is buffered, so there is nowhere to ask for approval", opts.parallelism()), Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "arguments"})
	}

	// Teardown is delete-only, so a node that declared anything short of approve = "all" has already said it must not be torn down unattended. This reads the declaration (graph.Node.Approve, "" when neither the node nor an enclosing use ever spoke) rather than approveFor's resolved level, which fills blanks down to ApproveSafe and would therefore refuse every node in an ordinary blueprint while a node that explicitly declared "none" still slipped through interactively — the inverse of the point. A declaration holds whether or not someone is watching, the same rule apply's gate follows. Checked before any lock is taken, so a refusal fails immediately rather than after waiting on whatever holds it.
	levels, err := e.executionLevels(opts, true)
	if err != nil {
		return result, err
	}
	for _, level := range levels {
		for _, name := range level {
			if a := e.Graph.Nodes[name].Approve; a != "" && a != blueprint.ApproveAll {
				return result, WithDiagnostic(fmt.Errorf("destroy: node %s declares approve = %q, which does not permit teardown; change it to %q on that node (or its enclosing use) if tearing it down is intended", name, a, blueprint.ApproveAll), Diagnostic{Code: "policy_blocked", Category: "policy", Phase: "policy"})
			}
		}
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

	session, err := e.startExecution("destroy", opts, true)
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

	e.logger().Info("destroy starting", "nodes", opts.Nodes, "parallelism", opts.parallelism(), "autoApprove", opts.AutoApprove)

	result.Nodes, resultErr = e.runLevels(opts, true, func(name string, applied map[string]exec.Outputs, out io.Writer) (exec.Outputs, string, error) {
		e, cancel := e.nodeEngine(opts)
		defer cancel()
		// A destroy plan needs the same resolved input values an apply would have used (e.g. a variable feeding a resource's count or for_each), so it's evaluated identically here: every upstream dependency is still standing at this point, since destroy walks the graph in reverse topological order (downstream first).
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

		r := &exec.Runner{Context: e.context(), Binary: e.runtimeFor(name), Dir: e.nodeDir(name), DataDir: e.dataDir(name), Env: e.envFor(name), Stdout: out, Stderr: out}
		// Unlike apply, there is no saved plan here for terragraph to ask about itself, so terraform's own confirmation is the approval — and it needs somewhere to read the answer from. Left nil when auto-approving, so an unattended run can never block on a question.
		var answered *countingReader
		if !opts.AutoApprove && e.Stdin != nil {
			// Wrapping a file makes os/exec copy stdin in a goroutine that can block Wait forever when the runtime exits without reading it.
			if input, ok := e.Stdin.(*os.File); ok {
				r.Stdin = input
			} else {
				answered = &countingReader{r: e.Stdin}
				r.Stdin = answered
			}
		}
		if dc := e.nodeContracts(name); dc != nil && len(dc.Consumer) > 0 {
			return nil, StatusDestroyed, e.destroyContractPlan(name, r, session, opts, exec.VarFileArgs(varsPath, vars))
		}
		if err := session.transition(name, "operating", "", ""); err != nil {
			return nil, "", err
		}
		if err := r.Destroy(opts.AutoApprove, exec.VarFileArgs(varsPath, vars)...); err != nil {
			err = session.fail(name, "indeterminate", err)
			// Direct file inheritance keeps reads inside Terraform, so a failed interactive run can only offer a conditional unattended-run remedy.
			if !opts.AutoApprove && answered == nil && e.Stdin != nil {
				return nil, "", fmt.Errorf("destroy: %w (if running unattended, pass --auto-approve to destroy without asking)", err)
			}
			if !opts.AutoApprove && (answered == nil || answered.n == 0) {
				return nil, "", fmt.Errorf("destroy: %w (nothing was available to read approval from; pass --auto-approve to destroy without asking)", err)
			}
			return nil, "", fmt.Errorf("destroy: %w", err)
		}
		if err := session.transition(name, "completed", "", ""); err != nil {
			return nil, "", err
		}
		return nil, StatusDestroyed, nil
	}, nil)
	return result, resultErr
}

// countingReader records whether anything was ever read from it, so a failed destroy can tell "nobody answered" from "the answer was rejected". Only ever handed to one subprocess at a time (destroy prompts require --parallelism 1), so it needs no locking.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// destroyContractPlan validates effective external inputs before executing the same native destroy plan, without demanding deleted outputs.
func (e *Engine) destroyContractPlan(name string, r *exec.Runner, session *executionSession, opts Options, args []string) error {
	if err := session.transition(name, "initializing", "", ""); err != nil {
		return err
	}
	if err := e.initNode(name, r); err != nil {
		return session.fail(name, "indeterminate", fmt.Errorf("init: %w", err))
	}
	if err := session.transition(name, "preparing", "", ""); err != nil {
		return err
	}
	// Initialize first so stale backend metadata cannot bypass the saved-plan requirement for effective input checks.
	if !r.SupportsSavedPlan() {
		return fmt.Errorf("node.%s: destroy with consumer contracts requires a local plan, which the %q backend cannot produce; use a state-storage backend (s3, gcs, azurerm, http, local) instead", name, r.BackendType())
	}
	path := e.planPath(name)
	cleanup, err := prepareSavedPlan(path)
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = r.PlanChanges(path, append([]string{"-destroy"}, args...)...)
	if err != nil {
		return fmt.Errorf("destroy plan: %w", err)
	}
	values, err := r.PlanValues(path)
	if err != nil {
		if policyErr := e.contractPolicy([]ContractCheck{{"node." + name, "destroy input evidence", blueprint.ContractDeferred}}, true); policyErr != nil {
			return fmt.Errorf("%w: %w", policyErr, err)
		}
	} else if err := e.contractPolicy(e.inputContractChecks(name, values), true); err != nil {
		return err
	}
	if !opts.AutoApprove {
		approved, err := e.approve(name, r.Stdout)
		if err != nil {
			return err
		}
		if !approved {
			return fmt.Errorf("destroy cancelled: node %s was not approved", name)
		}
	}
	if err := session.transition(name, "operating", "", ""); err != nil {
		return err
	}
	if err := r.ApplyPlan(path); err != nil {
		return session.fail(name, "indeterminate", fmt.Errorf("destroy: %w", err))
	}
	return session.transition(name, "completed", "", "")
}
