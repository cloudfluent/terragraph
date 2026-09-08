package engine

import (
	"fmt"
	"time"
)

// RecoverExecution either retries only output collection or retires an uncertain attempt after an operator has inspected actual state; it never replays a mutation.
func (e *Engine) RecoverExecution(id string, confirmStopped, stateReviewed, replan bool) (ExecutionRecord, error) {
	if !confirmStopped {
		return ExecutionRecord{}, fmt.Errorf("recovery requires --confirm-stopped after verifying the previous executor has stopped")
	}
	if replan && !stateReviewed {
		return ExecutionRecord{}, fmt.Errorf("--replan requires --state-reviewed after inspecting the actual backend state and affected resources")
	}
	unlock, err := e.lockRun()
	if err != nil {
		return ExecutionRecord{}, err
	}
	defer unlock()
	unlockGraph, err := e.lockGraph()
	if err != nil {
		return ExecutionRecord{}, err
	}
	defer unlockGraph()
	store, err := e.openExecutionStore()
	if err != nil {
		return ExecutionRecord{}, err
	}
	defer func() { _ = store.close() }()
	record, revision, err := readExecutionRecord(e.context(), store, id)
	if err != nil {
		return ExecutionRecord{}, err
	}
	scope, err := e.executionScope()
	if err != nil {
		return ExecutionRecord{}, err
	}
	if record.Scope != scope {
		return ExecutionRecord{}, fmt.Errorf("execution belongs to another coordination scope; select the original blueprint and lock")
	}
	if !executionNeedsRecovery(record) {
		return ExecutionRecord{}, fmt.Errorf("execution %s does not need recovery; inspect it with plan show", id)
	}
	session := &executionSession{engine: e, store: store, record: record, revision: revision}
	if replan {
		// Acknowledgement releases the coordination barrier without relabelling an unknown historical mutation as successful.
		now := time.Now().UTC()
		next := record
		next.Status, next.RecoveryAt, next.FinishedAt, next.UpdatedAt = "recovered_replan_required", &now, &now, now
		if err := session.publish(next); err != nil {
			return ExecutionRecord{}, err
		}
		return session.record, nil
	}
	for _, node := range record.Nodes {
		switch node.Phase {
		case "initializing", "applying", "operating", "indeterminate":
			return ExecutionRecord{}, fmt.Errorf("node.%s: mutation outcome is unknown; inspect actual state, then use --state-reviewed --replan to retire this attempt and create a fresh plan", node.Name)
		case "applied":
			target, err := e.executionTarget(node.Name)
			if err != nil {
				return ExecutionRecord{}, err
			}
			if target != node.Target {
				return ExecutionRecord{}, fmt.Errorf("node.%s: backend target changed; restore the original configuration before collecting outputs", node.Name)
			}
		}
	}
	for _, node := range record.Nodes {
		if node.Phase != "applied" {
			continue
		}
		outputs, err := e.runner(node.Name).Outputs()
		if err != nil {
			return ExecutionRecord{}, fmt.Errorf("node.%s: reading outputs without reapplying: %w", node.Name, err)
		}
		if err := e.writeSnapshot(node.Name, outputs); err != nil {
			return ExecutionRecord{}, err
		}
		if err := session.transition(node.Name, "completed", "outputs_recovered", ""); err != nil {
			return ExecutionRecord{}, err
		}
	}
	// Unstarted downstream work still needs a new plan, even when all known mutations have now been post-processed.
	now := time.Now().UTC()
	next := session.record
	next.Status, next.RecoveryAt, next.FinishedAt, next.UpdatedAt = "recovered_replan_required", &now, &now, now
	if err := session.publish(next); err != nil {
		return ExecutionRecord{}, err
	}
	return session.record, nil
}
