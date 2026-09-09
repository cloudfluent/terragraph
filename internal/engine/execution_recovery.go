package engine

import (
	"errors"
	"fmt"
	"time"
)

// RecoverExecution either retries only output collection or retires an uncertain attempt after an operator has inspected actual state; it never replays a mutation.
func (e *Engine) RecoverExecution(id string, confirmStopped, stateReviewed, replan bool, initializeBackend ...bool) (ExecutionRecord, error) {
	if !confirmStopped {
		return ExecutionRecord{}, WithDiagnostic(fmt.Errorf("recovery requires --confirm-stopped after verifying the previous executor has stopped"), Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "arguments", Remedy: "confirm that the previous executor has stopped before supplying --confirm-stopped"})
	}
	if replan && !stateReviewed {
		return ExecutionRecord{}, WithDiagnostic(fmt.Errorf("--replan requires --state-reviewed after inspecting the actual backend state and affected resources"), Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "arguments", Remedy: "inspect actual state before supplying --state-reviewed"})
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
		return record, err
	}
	if record.Scope != scope {
		return record, fmt.Errorf("execution belongs to another coordination scope; select the original blueprint and lock")
	}
	if !executionNeedsRecovery(record) {
		return record, fmt.Errorf("execution %s does not need recovery; inspect it with plan show", id)
	}
	session := &executionSession{engine: e, store: store, record: record, revision: revision}
	if replan {
		// Acknowledgement releases the coordination barrier without relabelling an unknown historical mutation as successful.
		now := time.Now().UTC()
		next := record
		next.Status, next.RecoveryAt, next.FinishedAt, next.UpdatedAt = "recovered_replan_required", &now, &now, now
		if err := session.publish(next); err != nil {
			return session.record, err
		}
		return session.record, nil
	}
	var issues []error
	if record.Preparation != "" {
		issues = append(issues, fmt.Errorf("backend preparation outcome is unknown; inspect state and use --state-reviewed --replan"))
	}
	for _, node := range record.Nodes {
		switch node.Phase {
		case "initializing", "applying", "operating", "indeterminate":
			issues = append(issues, fmt.Errorf("node.%s: mutation outcome is unknown; inspect actual state, then use --state-reviewed --replan", node.Name))
		case "applied":
			target, err := e.executionTarget(node.Name)
			if err != nil {
				issues = append(issues, err)
				continue
			}
			if target != node.Target {
				issues = append(issues, fmt.Errorf("node.%s: backend target changed; restore the original configuration", node.Name))
				continue
			}
			allowInit := len(initializeBackend) > 0 && initializeBackend[0]
			if err := e.recoverNodeOutputs(session, node.Name, allowInit); err != nil {
				issues = append(issues, err)
				continue
			}
			if err := session.transition(node.Name, "completed", "outputs_recovered", ""); err != nil {
				return session.record, err
			}
		}
	}
	if len(issues) > 0 {
		return session.record, errors.Join(issues...)
	}
	// Unstarted downstream work still needs a new plan, even when all known mutations have now been post-processed.
	now := time.Now().UTC()
	next := session.record
	next.Status, next.RecoveryAt, next.FinishedAt, next.UpdatedAt = "recovered_replan_required", &now, &now, now
	if err := session.publish(next); err != nil {
		return session.record, err
	}
	return session.record, nil
}
