package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cloudfluent/terragraph/internal/plugins"
)

// RecoverPluginCall resolves only a recorded external effect; infrastructure mutation is never replayed here.
func (e *Engine) RecoverPluginCall(id, callID string, confirmStopped, acknowledge bool) (ExecutionRecord, error) {
	if !confirmStopped {
		return ExecutionRecord{}, fmt.Errorf("plugin recovery requires --confirm-stopped after verifying the previous executor stopped")
	}
	unlock, err := e.lockRun()
	if err != nil {
		return ExecutionRecord{}, err
	}
	defer unlock()
	graphUnlock, err := e.lockGraph()
	if err != nil {
		return ExecutionRecord{}, err
	}
	defer graphUnlock()
	store, err := e.openExecutionStore()
	if err != nil {
		return ExecutionRecord{}, err
	}
	defer func() { _ = store.close() }()
	record, revision, err := readExecutionRecord(e.context(), store, id)
	if err != nil {
		return record, err
	}
	scope, err := e.executionScope()
	if err != nil {
		return record, err
	}
	if scope != record.Scope {
		return record, fmt.Errorf("execution belongs to another coordination scope; use the original blueprint")
	}
	index := -1
	for i, c := range record.PluginCalls {
		if c.ID == callID {
			index = i
			break
		}
	}
	if index < 0 {
		return record, fmt.Errorf("plugin call not found; select an ID from plan show")
	}
	call := record.PluginCalls[index]
	if call.Status == "acknowledged" {
		return record, fmt.Errorf("plugin call was already acknowledged")
	}
	outstandingLease := call.Cleanup != nil && call.Cleanup.Lease != nil && call.Event.Phase == "credential.acquire"
	if call.Status == "completed" && !outstandingLease {
		return record, fmt.Errorf("plugin call is already complete")
	}
	s := &executionSession{engine: e, store: store, record: record, revision: revision}
	if acknowledge {
		call.Status = "acknowledged"
		call.Code = "operator_reviewed_external_state"
		if err := s.recordPlugin(call); err != nil {
			return s.record, err
		}
	} else {
		m, err := plugins.NewLifecycle(e.context(), e.BaseDir, e.Blueprint.Plugins, call.Event.Operation, id, s.recordPlugin)
		if err != nil {
			return record, err
		}
		err = m.Replay(e.context(), call)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(e.context()), 30*time.Second)
		closeErr := m.CloseProcesses(ctx)
		cancel()
		err = errors.Join(err, closeErr)
		if err != nil {
			return s.record, err
		}
	}
	if record.Operation == "saved_apply" {
		err = s.finishSaved(nil)
	} else {
		err = s.finish(nil)
	}
	return s.record, err
}

// ReadPluginReport requires an explicit request because plugin-authored reports can contain sensitive plan evidence.
func (e *Engine) ReadPluginReport(id, callID string, out io.Writer) error {
	record, err := e.GetExecution(id)
	if err != nil {
		return err
	}
	for _, call := range record.PluginCalls {
		if call.ID == callID {
			if len(call.Report) == 0 {
				return fmt.Errorf("plugin call has no report")
			}
			_, err = out.Write(call.Report)
			return err
		}
	}
	return fmt.Errorf("plugin call not found; select an ID from plan show")
}
