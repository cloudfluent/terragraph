package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		return record, WithDiagnostic(fmt.Errorf("execution requires recovery; cancellation cannot establish that a mutation stopped"), Diagnostic{Code: "recovery_required", Category: "recovery", Phase: "recovery", RelatedExecutionID: record.ID, Remedy: "inspect plan show and recover after confirming the executor has stopped"})
	}
	if record.FinishedAt != nil {
		return record, e.cleanupExecution(store, record)
	}
	s := &executionSession{engine: e, store: store, record: record, revision: revision}
	now := time.Now().UTC()
	record.Status, record.UpdatedAt, record.FinishedAt = "cancelled", now, &now
	if err := s.publish(record); err != nil {
		return s.record, err
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
		if record.Scope != scope || executionNeedsRecovery(record) {
			continue
		}
		if record.Status == "waiting_for_apply" {
			expired, err := e.frontierExpired(store, record, now)
			if err != nil {
				return removed, err
			}
			if expired {
				record.Status, record.UpdatedAt, record.FinishedAt = "expired", now, &now
				s := &executionSession{engine: e, store: store, record: record, revision: revision}
				if err := s.publish(record); err != nil {
					return removed, err
				}
				revision = s.revision
			}
		}
		if record.FinishedAt == nil {
			continue
		}
		if err := e.cleanupExecution(store, record); err != nil {
			return removed, err
		}
		if now.Sub(*record.FinishedAt) < e.Blueprint.ExecutionSettings().RecordRetention {
			continue
		}
		backup, backupErr := store.read(e.context(), record.ID+".bin")
		if backupErr != nil && !errors.Is(backupErr, errExecutionMissing) {
			return removed, backupErr
		}
		if backupErr == nil {
			if err := store.remove(e.context(), record.ID+".bin", backup.Revision); err != nil {
				return removed, err
			}
		}
		if err := os.Remove(filepath.Join(e.BaseDir, ".terragraph", "backups", record.ID+".tfstate")); err != nil && !os.IsNotExist(err) {
			return removed, err
		}

		if err := store.remove(e.context(), key, revision); err != nil {
			return removed, err
		}
		removed = append(removed, record.ID)
	}
	return removed, nil
}

func (e *Engine) frontierExpired(store executionStore, record ExecutionRecord, now time.Time) (bool, error) {
	for _, node := range record.Nodes {
		if node.Phase != "planned" {
			continue
		}
		object, err := store.read(e.context(), node.PlanID+".bin")
		if err != nil {
			return false, err
		}
		var bundle planBundle
		if err := json.Unmarshal(object.Data, &bundle); err != nil {
			return false, err
		}
		if bundle.ID != node.PlanID || bundle.ExecutionID != record.ID || bundle.ExpiresAt.IsZero() {
			return false, fmt.Errorf("execution %s has an invalid plan reference; inspect the stored artifacts", record.ID)
		}
		if !now.Before(bundle.ExpiresAt) {
			return true, nil
		}
	}
	return false, nil
}
