package engine

import (
	"errors"
	"fmt"
	sdk "github.com/cloudfluent/terragraph/plugin"
	"os"
	"time"

	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
	"github.com/cloudfluent/terragraph/internal/plugins"
)

// SavePlans freezes only the current frontier; later inputs must come from real upstream outputs after the reviewed frontier has been applied.
func (e *Engine) SavePlans(opts Options, continueID string) (record ExecutionRecord, resultErr error) {
	if continueID != "" && opts.hasSelectionFlags() {
		return record, WithDiagnostic(fmt.Errorf("--continue already fixes node selection; omit --node and --downstream"), Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "arguments"})
	}
	if continueID == "" {
		var err error
		opts, err = e.resolveSelection(opts)
		if err != nil {
			return record, err
		}
		opts.announceSelection(false)
	}
	unlock, err := e.lockRun()
	if err != nil {
		return record, err
	}
	defer unlock()
	if continueID == "" {
		if err := e.checkRuntimeFiles(opts); err != nil {
			return record, err
		}
	}
	unlockGraph, err := e.lockGraph()
	if err != nil {
		return record, err
	}
	defer unlockGraph()
	var s *executionSession
	if continueID == "" {
		s, err = e.startExecution("saved_apply", opts, false)
	} else {
		s, err = e.openSavedExecution(continueID)
	}
	if err != nil {
		return record, err
	}
	defer s.close()
	defer func() { resultErr = errors.Join(resultErr, e.finishPlugins(resultErr)) }()
	record = s.record
	if continueID != "" {
		opts, err = e.storedSelection(opts, s.record)
		opts.announceSelection(false)
		if err != nil {
			return record, err
		}
		if err := e.checkRuntimeFiles(opts); err != nil {
			return record, err
		}
	}
	if s.record.Status != "preparing" && s.record.Status != "ready_for_next_plan" {
		return record, fmt.Errorf("execution %s is %s; apply its pending plans or start a fresh execution", s.record.ID, s.record.Status)
	}
	defer func() {
		resultErr = errors.Join(resultErr, e.finishPlugins(resultErr))
		if resultErr != nil {
			resultErr = errors.Join(resultErr, s.finishSaved(resultErr))
		}
		record = s.record
	}()
	binding, err := e.savedGraphBinding(s.record)
	if err != nil {
		return record, err
	}
	if s.record.Binding != "" && s.record.Binding != binding {
		return record, WithDiagnostic(fmt.Errorf("graph configuration changed; cancel this execution and create a fresh plan"), Diagnostic{Code: "saved_plan_incompatible", Category: "artifact", Phase: "artifact"})
	}
	if e.plugins == nil {
		names := []string{}
		for _, n := range s.record.Nodes {
			names = append(names, n.Name)
		}
		if err := e.startPlugins("plan", s, names); err != nil {
			return record, err
		}
	}
	next := s.record
	next.Binding = binding
	if err := s.publish(next); err != nil {
		return record, err
	}
	phases := map[string]string{}
	for _, node := range s.record.Nodes {
		phases[node.Name] = node.Phase
	}
	count := 0
	for _, node := range append([]ExecutionNode(nil), s.record.Nodes...) {
		if node.Phase != "pending" {
			continue
		}
		ready := true
		for _, upstream := range e.Graph.In[node.Name] {
			if phase, selected := phases[upstream]; selected && phase != "completed" {
				ready = false
			}
		}
		if !ready {
			continue
		}
		if err := e.saveFrontierNode(s, node.Name, opts); err != nil {
			return record, errors.Join(err, s.noteSavedFailure(node.Name, "plan_step_failed"))
		}
		if err := e.plugins.Emit(e.context(), sdk.Event{Phase: "node.finished", Node: node.Name, Status: StatusPlanned}); err != nil {
			return record, err
		}
		if err := e.plugins.CompletionError(); err != nil {
			return record, err
		}
		count++
	}
	if count == 0 {
		return record, fmt.Errorf("execution has no unplanned frontier; inspect plan show before continuing")
	}
	next = s.record
	next.Status = "waiting_for_apply"
	next.UpdatedAt = time.Now().UTC()
	return next, s.publish(next)
}

func (e *Engine) saveFrontierNode(s *executionSession, name string, opts Options) error {
	if err := e.plugins.Emit(e.context(), sdk.Event{Phase: "node.prepare", Node: name}); err != nil {
		return err
	}
	vars, err := e.resolveLiveInputs(name)
	if err != nil {
		return err
	}
	varsPath := e.tfVarsPath(name)
	if _, err := exec.WriteTFVars(varsPath, vars); err != nil {
		return err
	}
	defer func() { _ = os.Remove(varsPath) }()
	r := e.runner(name)
	if err := s.transition(name, "initializing", "", ""); err != nil {
		return err
	}
	if err := e.initNode(name, r); err != nil {
		return s.fail(name, "indeterminate", err)
	}
	if err := s.transition(name, "preparing", "", ""); err != nil {
		return err
	}
	if !r.SupportsSavedPlan() {
		return savedPlanUnsupportedError(name, r.BackendType())
	}
	before, err := e.planBinding(name, r, vars)
	if err != nil {
		return err
	}
	plan, err := e.prepareNodePlan(name, r, exec.VarFileArgs(varsPath, vars)...)
	if err != nil {
		return err
	}
	defer plan.cleanup()
	after, err := e.planBinding(name, r, vars)
	if err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("node.%s: source inputs changed while planning; create a fresh plan", name)
	}
	// Native show validates the produced artifact before publishing it as reviewable, including no-change plans.
	review := newPlanReview(e.approveFor(name, opts.Approve))
	_, review.Contracts, err = e.inspectContractPlan(name, r, plan.path, false)
	if err != nil {
		return err
	}
	review.Limitations = []string{"only this ready frontier is saved; downstream nodes require a new plan after applying upstream"}
	changes, err := r.PlanChangeSet(plan.path, &review.Outputs)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(e.Stdout, "node %s: %s\n", name, summarizeChanges(changes)); err != nil {
		return err
	}
	plan.binding = before
	_, err = e.retainPlan(s, plan, vars)
	if err != nil {
		return err
	}
	review.Resources = changes
	review.normalize(plan.changed)
	return s.setReview(name, review)
}

// ApplySavedPlans applies exactly the stored frontier and never plans or applies downstream nodes in the same invocation.
func (e *Engine) ApplySavedPlans(id string, opts Options) (result RunResult, resultErr error) {
	if opts.hasSelectionFlags() {
		return result, WithDiagnostic(fmt.Errorf("--plan already fixes node selection; omit --node and --downstream"), Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "arguments"})
	}
	if opts.parallelism() > 1 && !opts.AutoApprove {
		return result, WithDiagnostic(fmt.Errorf("--parallelism needs --auto-approve"), Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "arguments"})
	}
	unlock, err := e.lockRun()
	if err != nil {
		return result, err
	}
	defer unlock()
	unlockGraph, err := e.lockGraph()
	if err != nil {
		return result, err
	}
	defer unlockGraph()
	s, err := e.openSavedExecution(id)
	if err != nil {
		return result, err
	}
	result.ExecutionID = s.record.ID
	defer s.close()
	defer func() { resultErr = errors.Join(resultErr, e.finishPlugins(resultErr)) }()
	opts, err = e.storedSelection(opts, s.record)
	opts.announceSelection(false)
	if err != nil {
		return result, err
	}
	if err := e.checkRuntimeFiles(opts); err != nil {
		return result, err
	}
	if s.record.Status != "waiting_for_apply" {
		return result, fmt.Errorf("execution %s is %s; only waiting_for_apply can be applied", id, s.record.Status)
	}
	binding, err := e.savedGraphBinding(s.record)
	if err != nil {
		return result, err
	}
	if binding != s.record.Binding {
		return result, WithDiagnostic(fmt.Errorf("graph configuration changed; cancel this execution and create a fresh plan"), Diagnostic{Code: "saved_plan_incompatible", Category: "artifact", Phase: "artifact"})
	}
	defer func() {
		resultErr = errors.Join(resultErr, e.finishPlugins(resultErr))
		if resultErr != nil {
			resultErr = errors.Join(resultErr, s.finishSaved(resultErr))
		}
	}()
	names := []string{}
	for _, n := range s.record.Nodes {
		names = append(names, n.Name)
	}
	if err := e.startPlugins("apply", s, names); err != nil {
		return result, err
	}

	for _, node := range append([]ExecutionNode(nil), s.record.Nodes...) {
		if node.Phase != "planned" {
			continue
		}
		run := NodeRun{Node: node.Name, Level: 1}
		status, err := e.applySavedNode(s, node, opts)
		run.Status, run.Err = status, err
		run.Diagnostics = Diagnostics(err, Diagnostic{Code: "runtime_failed", Category: "runtime", Phase: "apply", Subject: "node." + node.Name})
		if err != nil {
			run.Status = StatusFailed
			result.Nodes = append(result.Nodes, run)
			return result, errors.Join(err, s.noteSavedFailure(node.Name, "apply_step_failed"))
		}
		result.Nodes = append(result.Nodes, run)
		if err := e.plugins.Emit(e.context(), sdk.Event{Phase: "node.finished", Node: node.Name, Status: status}); err != nil {
			return result, err
		}
		if err := e.plugins.CompletionError(); err != nil {
			return result, err
		}
	}
	next := s.record
	next.Status = "completed"
	for _, node := range next.Nodes {
		if node.Phase != "completed" {
			next.Status = "ready_for_next_plan"
		}
	}
	now := time.Now().UTC()
	next.UpdatedAt = now
	if next.Status == "completed" {
		next.FinishedAt = &now
	}
	if err := s.publish(next); err != nil {
		return result, err
	}
	if err := e.cleanupExecution(s.store, next); err != nil {
		e.logger().Warn("execution artifact cleanup deferred", "execution", next.ID, "error", err)
	}
	return result, nil
}

func (e *Engine) applySavedNode(s *executionSession, node ExecutionNode, opts Options) (string, error) {
	if err := e.plugins.Emit(e.context(), sdk.Event{Phase: "node.prepare", Node: node.Name}); err != nil {
		return StatusNotRun, err
	}
	bundle, err := e.readPlanBundle(s, node)
	if err != nil {
		return "", err
	}
	vars, err := e.resolveLiveInputs(node.Name)
	if err != nil {
		return "", err
	}
	r := e.runner(node.Name)
	// Refuse a changed target before init can point its cache at a different backend.
	target, err := e.executionTarget(node.Name)
	if err != nil {
		return "", err
	}
	if target != node.Target {
		return "", WithDiagnostic(fmt.Errorf("node.%s: backend target changed; create a fresh plan", node.Name), Diagnostic{Code: "saved_plan_incompatible", Category: "artifact", Phase: "artifact"})
	}
	binding, err := e.planBinding(node.Name, r, vars)
	if err != nil {
		return "", err
	}
	if binding != bundle.Binding {
		return "", WithDiagnostic(fmt.Errorf("node.%s: source, runtime, paths, or inputs changed; create a fresh plan", node.Name), Diagnostic{Code: "saved_plan_incompatible", Category: "artifact", Phase: "artifact"})
	}
	if err := s.transition(node.Name, "initializing", "", ""); err != nil {
		return "", err
	}
	if err := e.initNode(node.Name, r); err != nil {
		return "", s.fail(node.Name, "indeterminate", err)
	}
	if err := s.transition(node.Name, "planned", "", ""); err != nil {
		return "", err
	}
	after, err := e.planBinding(node.Name, r, vars)
	if err != nil {
		return "", err
	}
	if after != bundle.Binding {
		return "", fmt.Errorf("node.%s: runtime preparation changed plan bindings; create a fresh plan", node.Name)
	}
	path := e.planPath(node.Name)
	cleanup, err := prepareSavedPlan(path)
	if err != nil {
		return "", err
	}
	defer cleanup()
	if err := os.WriteFile(path, bundle.Plan, 0600); err != nil {
		return "", err
	}
	if err := e.pluginPlan(node.Name, r, path, "node.plan.ready"); err != nil {
		return "", err
	}
	plan := &preparedNodePlan{name: node.Name, runner: r, path: path, changed: bundle.Changed, cleanup: cleanup, session: s, verifyUnchanged: true}
	_, status, err := e.applyPreparedPlan(plan, opts)
	return status, err
}

func (e *Engine) openSavedExecution(id string) (*executionSession, error) {
	// Store availability and record integrity cannot establish that the saved plan itself is incompatible.
	readDiagnostic := Diagnostic{Code: "execution_read_failed", Category: "record", Phase: "history", Subject: "execution", RelatedExecutionID: id, Remedy: "check the selected execution store and requested ID; restore access or missing records before retrying"}
	store, err := e.openExecutionStore()
	if err != nil {
		return nil, WithDiagnostic(err, readDiagnostic)
	}
	record, revision, err := readExecutionRecord(e.context(), store, id)
	if err != nil {
		_ = store.close()
		return nil, WithDiagnostic(err, readDiagnostic)
	}
	scope, err := e.executionScope()
	if err != nil {
		_ = store.close()
		return nil, WithDiagnostic(err, Diagnostic{Code: "configuration_failed", Category: "configuration", Phase: "prepare", Subject: "execution", RelatedExecutionID: id, Remedy: "check the blueprint coordination scope configuration before retrying"})
	}
	if record.Scope != scope || record.Operation != "saved_apply" || record.RecoveryAt != nil || executionNeedsRecovery(record) {
		_ = store.close()
		return nil, WithDiagnostic(fmt.Errorf("execution cannot resume in its current scope or recovery status; inspect plan show"), Diagnostic{Code: "saved_plan_incompatible", Category: "artifact", Phase: "artifact", Subject: "execution", RelatedExecutionID: id, Remedy: "inspect plan show and restore the original scope or resolve its recovery status before continuing"})
	}
	if err := e.checkExecutionBarrier(store, scope, id); err != nil {
		_ = store.close()
		return nil, WithDiagnostic(err, readDiagnostic)
	}
	return &executionSession{engine: e, store: store, record: record, revision: revision}, nil
}

func (e *Engine) savedGraphBinding(record ExecutionRecord) (string, error) {
	nodes := map[string]any{}
	for _, node := range record.Nodes {
		actual := e.Graph.Nodes[node.Name]
		if actual == nil {
			return "", fmt.Errorf("node.%s: removed from graph; create a fresh plan", node.Name)
		}
		files, err := e.planSourceFiles(node.Name)
		if err != nil {
			return "", err
		}
		// Pending nodes may acquire their provider lockfile during their first init; each published bundle binds that lockfile separately.
		delete(files, ".terraform.lock.hcl")
		nodes[node.Name] = struct {
			Declaration any
			Runtime     any
			Env         any
			Approve     any
			Binary      exec.Binary
			Files       map[string]string
		}{actual.Node, actual.Runtime, actual.Env, actual.Approve, e.runtimeFor(node.Name), files}
	}
	contracts, err := e.Graph.Contracts.Digest()
	if err != nil {
		return "", err
	}
	binding, err := executionDigest(struct {
		Contracts, ContractMode string
		Nodes                   map[string]any
		Edges                   any
	}{contracts, e.Graph.ContractMode, nodes, e.Graph.Edges})
	if err != nil {
		return "", err
	}
	if e.Blueprint != nil && len(e.Blueprint.Plugins) > 0 {
		p, err := plugins.Binding(e.BaseDir, e.Blueprint.Plugins)
		if err != nil {
			return "", err
		}
		return executionDigest([]string{binding, p})
	}
	return binding, nil
}

func (e *Engine) resolveLiveInputs(name string) (map[string]any, error) {
	outputs := map[string]exec.Outputs{}
	for _, edge := range e.Graph.Edges {
		if !edge.IsDataEdge() || edge.To.Node != name {
			continue
		}
		if _, ok := outputs[edge.From.Node]; ok {
			continue
		}
		live, err := e.runner(edge.From.Node).Outputs()
		if err != nil {
			return nil, fmt.Errorf("node.%s: retained plans require live upstream outputs; initialize the upstream backend and restore access: %w", name, err)
		}
		outputs[edge.From.Node] = live
	}
	return e.resolveInputs(name, outputs)
}

// finishSaved preserves resumable peers after a pre-mutation failure instead of treating a partial frontier as a disposable terminal run.
func (s *executionSession) finishSaved(runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.FinishedAt != nil && !executionNeedsRecovery(s.record) {
		return nil
	}
	next := s.record
	next.Nodes = append([]ExecutionNode(nil), next.Nodes...)
	next.UpdatedAt = time.Now().UTC()
	next.FinishedAt = nil
	next.Status = "ready_for_next_plan"
	planned, pending := false, false
	for i := range next.Nodes {
		if next.Nodes[i].Phase == "preparing" {
			next.Nodes[i].Phase = "pending"
		}
		planned = planned || next.Nodes[i].Phase == "planned"
		pending = pending || next.Nodes[i].Phase == "pending"
	}
	if executionNeedsRecovery(next) {
		next.Status = "needs_recovery"
	} else if planned {
		next.Status = "waiting_for_apply"
	} else if !pending {
		next.Status = "completed"
		if runErr != nil {
			next.Status = "failed"
		}
		next.FinishedAt = &next.UpdatedAt
	}
	return s.publish(next)
}

func (s *executionSession) noteSavedFailure(name, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.record
	next.Nodes = append([]ExecutionNode(nil), next.Nodes...)
	for i := range next.Nodes {
		if next.Nodes[i].Name == name {
			next.Nodes[i].Code = code
			return s.publish(next)
		}
	}
	return fmt.Errorf("node.%s: absent from execution", name)
}

// storedSelection uses record membership even for old records whose original selection intent is unknown.
func (e *Engine) storedSelection(opts Options, record ExecutionRecord) (Options, error) {
	opts.selection = record.Selection
	names := make([]string, 0, len(record.Nodes))
	for _, node := range record.Nodes {
		names = append(names, node.Name)
	}
	levels, err := graph.SelectedLevels(e.Graph, names, false)
	if err != nil {
		return opts, err
	}
	opts.selection, opts.levels = record.Selection, levels
	return opts, nil
}
