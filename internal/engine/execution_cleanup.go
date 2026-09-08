package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// cleanupExecution removes only artifacts attached to a resolved terminal record; unknown outcomes remain evidence regardless of age.
func (e *Engine) cleanupExecution(store executionStore, record ExecutionRecord) error {
	if record.FinishedAt == nil || executionNeedsRecovery(record) {
		return nil
	}
	for _, node := range record.Nodes {
		if node.PlanID == "" {
			continue
		}
		key := node.PlanID + ".bin"
		object, err := store.read(e.context(), key)
		if errors.Is(err, errExecutionMissing) {
			continue
		}
		if err != nil {
			return err
		}
		if err := store.remove(e.context(), key, object.Revision); err != nil && !errors.Is(err, errExecutionMissing) {
			return err
		}
	}
	return nil
}

// CancelExecution invalidates a paused or failed attempt before its artifacts become eligible for deletion; it cannot cancel a potentially live mutation.
func (e *Engine) CancelExecution(id string) (ExecutionRecord, error) {
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
		return record, fmt.Errorf("execution belongs to another coordination scope; restore the original configuration")
	}
	if executionNeedsRecovery(record) {
		return record, fmt.Errorf("execution requires recovery; cancellation cannot establish that a mutation stopped")
	}
	if record.FinishedAt != nil {
		return record, e.cleanupExecution(store, record)
	}
	now := time.Now().UTC()
	record.Status, record.UpdatedAt, record.FinishedAt = "cancelled", now, &now
	s := &executionSession{engine: e, store: store, record: record, revision: revision}
	if err := s.publish(record); err != nil {
		return record, err
	}
	return record, e.cleanupExecution(store, record)
}

// PruneExecutions enforces terminal retention under the same coordination locks; it never interprets age or an absent journal as proof of safety.
func (e *Engine) PruneExecutions() ([]string, error) {
	unlock, err := e.lockRun()
	if err != nil {
		return nil, err
	}
	defer unlock()
	unlockGraph, err := e.lockGraph()
	if err != nil {
		return nil, err
	}
	defer unlockGraph()
	store, err := e.openExecutionStore()
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.close() }()
	return e.pruneExecutions(store)
}

func (e *Engine) pruneExecutions(store executionStore) ([]string, error) {
	keys, err := store.list(e.context())
	if err != nil {
		return nil, err
	}
	scope, err := e.executionScope()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	removed := []string{}
	for _, key := range keys {
		if !strings.HasPrefix(key, "run-") || !strings.HasSuffix(key, ".json") {
			continue
		}
		record, revision, err := readExecutionRecord(e.context(), store, strings.TrimSuffix(key, ".json"))
		if err != nil {
			return removed, err
		}
		if record.Scope != scope || executionNeedsRecovery(record) || record.FinishedAt == nil {
			continue
		}
		if err := e.cleanupExecution(store, record); err != nil {
			return removed, err
		}
		if now.Sub(*record.FinishedAt) < e.Blueprint.ExecutionSettings().RecordRetention {
			continue
		}
		if err := store.remove(e.context(), key, revision); err != nil {
			return removed, err
		}
		removed = append(removed, record.ID)
	}
	return removed, nil
}
