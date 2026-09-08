package engine

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// SavePlans freezes only the current frontier; later inputs must come from real upstream outputs after the reviewed frontier has been applied.
func (e *Engine) SavePlans(opts Options, continueID string) (record ExecutionRecord, resultErr error) {
	unlock, err := e.lockRun()
	if err != nil {
		return record, err
	}
	defer unlock()
	if err := e.checkRuntimeFiles(opts); err != nil {
		return record, err
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
	if continueID != "" && opts.Node != "" {
		return record, fmt.Errorf("--continue already fixes node selection; omit --node")
	}
	if s.record.Status != "preparing" && s.record.Status != "ready_for_next_plan" {
		return record, fmt.Errorf("execution %s is %s; apply its pending plans or start a fresh execution", s.record.ID, s.record.Status)
	}
	defer func() {
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
		return record, fmt.Errorf("graph configuration changed; cancel this execution and create a fresh plan")
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
func (e *Engine) ApplySavedPlans(id string, opts Options) (runs []NodeRun, resultErr error) {
	if opts.Node != "" {
		return nil, fmt.Errorf("--plan already fixes node selection; omit --node")
	}
	if opts.parallelism() > 1 && !opts.AutoApprove {
		return nil, fmt.Errorf("--parallelism needs --auto-approve")
	}
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
	s, err := e.openSavedExecution(id)
	if err != nil {
		return nil, err
	}
	defer s.close()
	if s.record.Status != "waiting_for_apply" {
		return nil, fmt.Errorf("execution %s is %s; only waiting_for_apply can be applied", id, s.record.Status)
	}
	binding, err := e.savedGraphBinding(s.record)
	if err != nil {
		return nil, err
	}
	if binding != s.record.Binding {
		return nil, fmt.Errorf("graph configuration changed; cancel this execution and create a fresh plan")
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, s.finishSaved(resultErr))
		}
	}()
	for _, node := range append([]ExecutionNode(nil), s.record.Nodes...) {
		if node.Phase != "planned" {
			continue
		}
		run := NodeRun{Node: node.Name, Level: 1}
		status, err := e.applySavedNode(s, node, opts)
		run.Status, run.Err = status, err
		if err != nil {
			run.Status = StatusFailed
			return append(runs, run), errors.Join(err, s.noteSavedFailure(node.Name, "apply_step_failed"))
		}
		runs = append(runs, run)
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
		return runs, err
	}
	return runs, e.cleanupExecution(s.store, next)
}

func (e *Engine) applySavedNode(s *executionSession, node ExecutionNode, opts Options) (string, error) {
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
		return "", fmt.Errorf("node.%s: backend target changed; create a fresh plan", node.Name)
	}
	binding, err := e.planBinding(node.Name, r, vars)
	if err != nil {
		return "", err
	}
	if binding != bundle.Binding {
		return "", fmt.Errorf("node.%s: source, runtime, paths, or inputs changed; create a fresh plan", node.Name)
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
	plan := &preparedNodePlan{name: node.Name, runner: r, path: path, changed: bundle.Changed, cleanup: cleanup, session: s, verifyUnchanged: true}
	_, status, err := e.applyPreparedPlan(plan, opts)
	return status, err
}

func (e *Engine) openSavedExecution(id string) (*executionSession, error) {
	store, err := e.openExecutionStore()
	if err != nil {
		return nil, err
	}
	record, revision, err := readExecutionRecord(e.context(), store, id)
	if err != nil {
		_ = store.close()
		return nil, err
	}
	scope, err := e.executionScope()
	if err != nil {
		_ = store.close()
		return nil, err
	}
	if record.Scope != scope || record.Operation != "saved_apply" || record.RecoveryAt != nil || executionNeedsRecovery(record) {
		_ = store.close()
		return nil, fmt.Errorf("execution cannot resume in its current scope or recovery status; inspect plan show")
	}
	if err := e.checkExecutionBarrier(store, scope, id); err != nil {
		_ = store.close()
		return nil, err
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
	return executionDigest(struct {
		Nodes map[string]any
		Edges any
	}{nodes, e.Graph.Edges})
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
