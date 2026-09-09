package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudfluent/terragraph/internal/graph"
)

// ExecutionRecord records observed command outcomes, never a second copy of infrastructure state.
type ExecutionRecord struct {
	// Selection is explanatory only; Nodes remains authoritative for saved execution membership.
	Selection     *graph.Selection `json:"selection,omitempty"`
	SchemaVersion int              `json:"schema_version"`
	Preparation   string           `json:"preparation,omitempty"`
	Backup        bool             `json:"backup,omitempty"`
	ID            string           `json:"id"`
	Scope         string           `json:"scope"`
	Binding       string           `json:"binding,omitempty"`
	Operation     string           `json:"operation"`
	Status        string           `json:"status"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	FinishedAt    *time.Time       `json:"finished_at,omitempty"`
	RecoveryAt    *time.Time       `json:"recovery_at,omitempty"`
	Nodes         []ExecutionNode  `json:"nodes"`
}

// ExecutionNode separates successful mutation from output collection so failed post-processing cannot cause a blind replay.
type ExecutionNode struct {
	Name   string      `json:"name"`
	Review *PlanReview `json:"review,omitempty"`
	Target string      `json:"target"`
	Phase  string      `json:"phase"`
	PlanID string      `json:"plan_id,omitempty"`
	Code   string      `json:"code,omitempty"`
}

// executionSession publishes each transition before making its in-memory decision available to another node worker.
type executionSession struct {
	engine   *Engine
	store    executionStore
	record   ExecutionRecord
	revision string
	mu       sync.Mutex
}

func executionDigest(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (e *Engine) executionScope() (string, error) {
	if e.Blueprint != nil && e.Blueprint.Lock != nil {
		return executionDigest(e.Blueprint.Lock)
	}
	return executionDigest(filepath.Clean(e.BaseDir))
}

func (e *Engine) executionTarget(name string) (string, error) {
	node := e.Graph.Nodes[name]
	if node == nil {
		return "", fmt.Errorf("node.%s: unknown node; select an expanded leaf", name)
	}
	config := map[string]string{}
	backend := "local"
	if node.Schema != nil {
		if node.Schema.Backend != "" {
			backend = node.Schema.Backend
		}
		for key, value := range node.Schema.BackendConfig {
			config[key] = value
		}
	}
	for key, value := range node.BackendConfig {
		config[key] = value
	}
	// Only backend-location fields contribute; credential rotation must not create a new coordination target.
	identity := map[string]string{"backend": backend}
	for _, key := range []string{"bucket", "key", "workspace_key_prefix", "region", "endpoint", "address", "path", "hostname", "organization", "resource_group_name", "storage_account_name", "container_name", "prefix"} {
		if value, ok := config[key]; ok {
			identity[key] = value
		}
	}
	workspace, err := e.executionWorkspace(name)
	if err != nil {
		return "", err
	}
	identity["workspace"] = workspace
	if backend == "http" && identity["address"] == "" {
		address, _, err := e.runner(name).EnvironmentValue("TF_HTTP_ADDRESS")
		if err != nil {
			return "", err
		}
		identity["address"] = address
	}
	return executionDigest(identity)
}

func (e *Engine) openExecutionStore() (executionStore, error) {
	cfg := e.Blueprint.ExecutionSettings()
	if cfg.Bucket == "" {
		return openLocalExecutionStore(e.BaseDir)
	}
	if e.Blueprint.Lock == nil {
		return nil, fmt.Errorf("execution: S3 storage requires a shared graph lock")
	}
	if e.Graph != nil {
		for name, node := range e.Graph.Nodes {
			if node.Schema == nil || node.Schema.Backend != "s3" {
				continue
			}
			bucket, key := node.Schema.BackendConfig["bucket"], node.Schema.BackendConfig["key"]
			if value, ok := node.BackendConfig["bucket"]; ok {
				bucket = value
			}
			if value, ok := node.BackendConfig["key"]; ok {
				key = value
			}
			if bucket == cfg.Bucket && (key == cfg.Prefix || strings.HasPrefix(key, cfg.Prefix+"/")) {
				return nil, fmt.Errorf("node.%s: state is inside execution.prefix; use a separate artifact prefix", name)
			}
		}
	}

	return openS3ExecutionStore(e.context(), cfg.Bucket, cfg.Prefix, cfg.Region)
}

func readExecutionRecord(ctx context.Context, store executionStore, id string) (ExecutionRecord, string, error) {
	if !strings.HasPrefix(id, "run-") {
		return ExecutionRecord{}, "", fmt.Errorf("invalid execution ID; select a run ID from plan list")
	}
	obj, err := store.read(ctx, id+".json")
	if err != nil {
		return ExecutionRecord{}, "", err
	}
	var record ExecutionRecord
	if err := json.Unmarshal(obj.Data, &record); err != nil {
		return ExecutionRecord{}, "", fmt.Errorf("reading execution record: %w", err)
	}
	if record.SchemaVersion != 1 || record.ID != id || record.Scope == "" || record.Status == "" {
		return ExecutionRecord{}, "", fmt.Errorf("execution record is incompatible or incomplete; restore a valid record")
	}
	if record.Selection != nil {
		names := make([]string, 0, len(record.Nodes))
		for _, node := range record.Nodes {
			names = append(names, node.Name)
		}
		if err := record.Selection.ValidateMembership(names); err != nil {
			return ExecutionRecord{}, "", err
		}
	}
	return record, obj.Revision, nil
}

func executionNeedsRecovery(record ExecutionRecord) bool {
	if record.RecoveryAt != nil {
		return false
	}
	if record.Preparation != "" {
		return true
	}
	for _, node := range record.Nodes {
		switch node.Phase {
		case "initializing", "applying", "operating", "indeterminate", "applied":
			return true
		}
	}
	return false
}

func (e *Engine) beginExecution(operation string, names []string, selection *graph.Selection, readOnly ...bool) (*executionSession, error) {
	store, err := e.openExecutionStore()
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = store.close()
		}
	}()
	scope, err := e.executionScope()
	if err != nil {
		return nil, err
	}
	if err := e.checkExecutionBarrier(store, scope, "", readOnly...); err != nil {
		return nil, err
	}
	if _, err := e.pruneExecutions(store); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	record := ExecutionRecord{Selection: selection, SchemaVersion: 1, ID: newExecutionID("run"), Scope: scope, Operation: operation, Status: "preparing", CreatedAt: now, UpdatedAt: now, Nodes: []ExecutionNode{}}
	for _, name := range names {
		target, err := e.executionTarget(name)
		if err != nil {
			return nil, err
		}
		record.Nodes = append(record.Nodes, ExecutionNode{Name: name, Target: target, Phase: "pending"})
	}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	rev, err := store.write(e.context(), record.ID+".json", data, "")
	if err != nil {
		return nil, WithDiagnostic(fmt.Errorf("creating execution record: %w", err), Diagnostic{Code: "execution_record_write_failed", Category: "record", Phase: "record", RelatedExecutionID: record.ID, Remedy: "inspect the execution store before retrying"})
	}
	failed = false
	return &executionSession{engine: e, store: store, record: record, revision: rev}, nil
}

func (s *executionSession) close() { _ = s.store.close() }

func (s *executionSession) transition(name, phase, code, planID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.record
	next.Nodes = append([]ExecutionNode(nil), s.record.Nodes...)
	found := false
	for i := range next.Nodes {
		if next.Nodes[i].Name == name {
			next.Nodes[i].Phase = phase
			next.Nodes[i].Code = code
			if planID != "" {
				next.Nodes[i].PlanID = planID
			}
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("node.%s: absent from execution; reload the execution", name)
	}
	next.UpdatedAt = time.Now().UTC()
	return s.publish(next)
}

func (s *executionSession) publish(next ExecutionRecord) error {
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	// Record a runtime's observed result even after Ctrl-C; a bounded independent context prevents losing it to the cancelled command context.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.engine.context()), 10*time.Second)
	defer cancel()
	revision, err := s.store.write(ctx, next.ID+".json", data, s.revision)
	if err != nil {
		return WithDiagnostic(fmt.Errorf("recording execution %s: %w", next.ID, err), Diagnostic{Code: "execution_record_write_failed", Category: "record", Phase: "record", RelatedExecutionID: next.ID, Remedy: "inspect plan show and recover any uncertain mutation before retrying"})
	}
	s.record, s.revision = next, revision
	return nil
}

func (s *executionSession) finish(runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.record
	now := time.Now().UTC()
	next.UpdatedAt = now
	next.Status = "completed"
	if runErr != nil {
		next.Status = "failed"
	}
	if executionNeedsRecovery(next) {
		next.Status = "needs_recovery"
	} else {
		next.FinishedAt = &now
	}
	if err := s.publish(next); err != nil {
		return err
	}
	if err := s.engine.cleanupExecution(s.store, next); err != nil {
		s.engine.logger().Warn("execution artifact cleanup deferred", "execution", next.ID, "error", err)
	}
	return nil
}

// ListExecutions is observational with respect to infrastructure, but retains local source coordination while reading protected records.
func (e *Engine) ListExecutions() ([]ExecutionRecord, error) {
	unlock, err := e.lockRun()
	if err != nil {
		return nil, err
	}
	defer unlock()
	store, err := e.openExecutionStore()
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.close() }()
	scope, err := e.executionScope()
	if err != nil {
		return nil, err
	}
	keys, err := store.list(e.context())
	if err != nil {
		return nil, err
	}
	records := []ExecutionRecord{}
	var issues []error
	for _, key := range keys {
		if !strings.HasPrefix(key, "run-") || !strings.HasSuffix(key, ".json") {
			continue
		}
		record, _, err := readExecutionRecord(e.context(), store, strings.TrimSuffix(key, ".json"))
		if err != nil {
			issues = append(issues, fmt.Errorf("execution %s: %w", strings.TrimSuffix(key, ".json"), err))
			continue
		}
		records = append(records, record)
		if record.Scope != scope {
			issues = append(issues, fmt.Errorf("execution %s belongs to a different coordination scope; restore the original lock configuration or use a dedicated prefix", record.ID))
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID < records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	return records, errors.Join(issues...)
}

func (s *executionSession) fail(name, phase string, cause error) error {
	return errors.Join(cause, s.transition(name, phase, "runtime_failed", ""))
}

func (e *Engine) startExecution(operation string, opts Options, reverse bool) (*executionSession, error) {
	levels, err := e.executionLevels(opts, reverse)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, level := range levels {
		names = append(names, level...)
	}
	return e.beginExecution(operation, names, opts.selection)
}

// GetExecution reads only the requested object so corrupt siblings cannot hide recovery evidence.
func (e *Engine) GetExecution(id string) (ExecutionRecord, error) {
	unlock, err := e.lockRun()
	if err != nil {
		return ExecutionRecord{}, err
	}
	defer unlock()
	store, err := e.openExecutionStore()
	if err != nil {
		return ExecutionRecord{}, err
	}
	defer func() { _ = store.close() }()
	record, _, err := readExecutionRecord(e.context(), store, id)
	if err != nil {
		return ExecutionRecord{}, fmt.Errorf("execution %s: %w", id, err)
	}
	scope, err := e.executionScope()
	if err != nil {
		return record, err
	}
	if record.Scope != scope {
		return record, fmt.Errorf("execution %s belongs to a different coordination scope; restore the original lock configuration or use a dedicated prefix", id)
	}
	return record, nil
}

func (e *Engine) checkExecutionBarrier(store executionStore, scope, exceptID string, readOnly ...bool) error {
	keys, err := store.list(e.context())
	if err != nil {
		return err
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "run-") || !strings.HasSuffix(key, ".json") {
			continue
		}
		old, _, err := readExecutionRecord(e.context(), store, strings.TrimSuffix(key, ".json"))
		if err != nil {
			return err
		}
		if old.Scope != scope {
			return WithDiagnostic(fmt.Errorf("execution %s belongs to another coordination scope; configure the same graph lock or separate prefixes", old.ID), Diagnostic{Code: "execution_scope_mismatch", Category: "configuration", Phase: "prepare", RelatedExecutionID: old.ID, Remedy: "configure the same graph lock or separate execution prefixes"})
		}
		if old.ID != exceptID && executionNeedsRecovery(old) && (len(readOnly) == 0 || !readOnly[0]) {
			return WithDiagnostic(fmt.Errorf("execution %s has an unresolved mutation; inspect it with plan show and recover before changing infrastructure", old.ID), Diagnostic{Code: "recovery_required", Category: "recovery", Phase: "prepare", RelatedExecutionID: old.ID, Remedy: "inspect plan show and recover before changing infrastructure"})
		}
	}
	return nil
}

func (s *executionSession) setReview(name string, review *PlanReview) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.record
	next.Nodes = append([]ExecutionNode(nil), next.Nodes...)
	for i := range next.Nodes {
		if next.Nodes[i].Name == name {
			next.Nodes[i].Review = review
			return s.publish(next)
		}
	}
	return fmt.Errorf("node.%s: absent from execution", name)
}

func (e *Engine) executionWorkspace(name string) (string, error) {
	workspace, _, err := e.runner(name).EnvironmentValue("TF_WORKSPACE")
	if err != nil {
		return "", err
	}
	if workspace == "" {
		data, err := os.ReadFile(filepath.Join(e.dataDir(name), "environment"))
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("node.%s: reading selected workspace: %w", name, err)
		}
		workspace = strings.TrimSpace(string(data))
	}
	if workspace == "" {
		workspace = "default"
	}
	return workspace, nil
}
