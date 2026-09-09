package engine

import (
	"errors"
	"fmt"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// preparedNodePlan keeps the inspected bytes and runtime together so applying a plan cannot silently create a different one.
type preparedNodePlan struct {
	name            string
	runner          *exec.Runner
	path            string
	changed         bool
	cleanup         func()
	session         *executionSession
	binding         string
	verifyUnchanged bool
}

func (e *Engine) prepareNodePlan(name string, runner *exec.Runner, args ...string) (*preparedNodePlan, error) {
	path := e.planPath(name)
	cleanup, err := prepareSavedPlan(path)
	if err != nil {
		return nil, err
	}
	changed, err := runner.PlanChanges(path, args...)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("plan: %w", err)
	}
	return &preparedNodePlan{name: name, runner: runner, path: path, changed: changed, cleanup: cleanup}, nil
}

// applyPreparedPlan keeps policy, confirmation and post-apply reads identical for every producer of a prepared plan.
func (e *Engine) applyPreparedPlan(plan *preparedNodePlan, opts Options) (exec.Outputs, string, error) {
	name, out := plan.name, plan.runner.Stdout
	evidence, _, err := e.inspectContractPlan(name, plan.runner, plan.path, true)
	if err != nil {
		return nil, "", err
	}
	if !plan.changed && !plan.verifyUnchanged {
		e.logger().Debug("plan reports no changes, skipping apply", "node", name)
		_, _ = fmt.Fprintf(out, "node %s: unchanged, skipping apply\n", name)
		outputs, err := plan.runner.Outputs()
		if err != nil {
			return nil, "", fmt.Errorf("plan says unchanged but outputs are unreadable: %w", err)
		}
		outputs = e.completePlanOutputs(name, outputs, evidence)
		if err := e.validateOutputContracts(name, outputs); err != nil {
			return nil, "", err
		}
		if err := e.writeSnapshot(name, outputs); err != nil {
			return nil, "", err
		}
		if err := plan.record("completed"); err != nil {
			return nil, "", err
		}
		return outputs, StatusUnchanged, nil
	}

	// What the plan actually does, read back from the file before any of it happens. Local only: no state is refreshed and no provider is called.
	changeSet, err := plan.runner.PlanChangeSet(plan.path)
	if err != nil {
		return nil, "", fmt.Errorf("reading plan: %w", err)
	}
	_, _ = fmt.Fprintf(out, "node %s: %s\n", name, summarizeChanges(changeSet))

	// Levels run in order, so refusing here means nothing downstream runs either: the cascade is cut at the node that caused it rather than audited after the fact.
	level := e.approveFor(name, opts.Approve)
	if blocked := notPermitted(changeSet, level); len(blocked) > 0 {
		return nil, "", e.gateError(name, level, blocked)
	}

	// The plan Terraform just printed is the plan about to be applied, so this asks about something the user has actually seen — which is the whole reason approval belongs here rather than inside a second `apply` that would plan again from scratch.
	if !opts.AutoApprove {
		approved, err := e.approve(name, out)
		if err != nil {
			return nil, "", err
		}
		if !approved {
			return nil, "", fmt.Errorf("apply cancelled: node %s was not approved", name)
		}
	}
	if err := plan.record("applying"); err != nil {
		return nil, "", err
	}
	if err := plan.runner.ApplyPlan(plan.path); err != nil {
		return nil, "", errors.Join(fmt.Errorf("apply: %w", err), plan.record("indeterminate"))
	}

	if err := plan.record("applied"); err != nil {
		return nil, "", err
	}
	outputs, err := plan.runner.Outputs()
	if err != nil {
		return nil, "", fmt.Errorf("reading outputs after apply: %w", err)
	}
	// Both exits that produce current reality publish the same snapshot (the unchanged branch does too), so nothing about the file reveals which path wrote it.
	outputs = e.completePlanOutputs(name, outputs, evidence)
	if err := e.validateOutputContracts(name, outputs); err != nil {
		return nil, "", err
	}
	if err := e.writeSnapshot(name, outputs); err != nil {
		return nil, "", err
	}
	if err := plan.record("completed"); err != nil {
		return nil, "", err
	}
	return outputs, StatusApplied, nil
}

func (p *preparedNodePlan) record(phase string) error {
	if p.session == nil {
		return nil
	}
	return p.session.transition(p.name, phase, "", "")
}
