package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
	"github.com/cloudfluent/terragraph/internal/plugins"
	sdk "github.com/cloudfluent/terragraph/plugin"
)

func pluginGraph(g *graph.Graph) []sdk.GraphNode {
	result := []sdk.GraphNode{}
	for name, n := range g.Nodes {
		inputs := []string{}
		if n.Schema != nil {
			for input := range n.Schema.Variables {
				inputs = append(inputs, input)
			}
		}
		sort.Strings(inputs)
		result = append(result, sdk.GraphNode{Name: name, Source: n.Source, Inputs: inputs, Dependencies: append([]string(nil), g.In[name]...)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func validatePluginGraph(ctx context.Context, m *plugins.Lifecycle, g *graph.Graph) error {
	if m == nil {
		for name, n := range g.Nodes {
			if len(n.Inputs)+len(n.Credentials) > 0 {
				return fmt.Errorf("node.%s: plugin binding has no root plugin declaration", name)
			}
		}
		return nil
	}
	status := "valid"
	for _, p := range graph.Validate(g) {
		if p.IsError() {
			status = "invalid"
			break
		}
	}
	for name, node := range g.Nodes {
		if err := m.ValidateBindings(name, node.Inputs, node.Credentials, node.Env); err != nil {
			return err
		}
	}
	return m.Emit(ctx, sdk.Event{Phase: "graph.validate", Graph: pluginGraph(g), Status: status})
}

func (e *Engine) startPlugins(operation string, s *executionSession, names []string) error {
	if e.Blueprint == nil || len(e.Blueprint.Plugins) == 0 {
		return nil
	}
	var record func(sdk.CallRecord) error
	id := ""
	if s != nil {
		id = s.record.ID
		record = s.recordPlugin
	}
	ctx := e.context()
	if e.Logger != nil {
		ctx = plugins.WithLogger(ctx, e.Logger)
	}
	m, err := plugins.NewLifecycle(ctx, e.BaseDir, e.Blueprint.Plugins, operation, id, record)
	if err != nil {
		return err
	}
	e.plugins, e.pluginOperation, e.pluginExecution = m, operation, s
	if err := m.Emit(e.context(), sdk.Event{Phase: "selection.ready", Nodes: names, Graph: pluginGraph(e.Graph)}); err != nil {
		return err
	}
	phase := "run.prepare"
	if s == nil {
		phase = "observation.prepare"
	}
	return m.Emit(e.context(), sdk.Event{Phase: phase, Nodes: names})
}

func (e *Engine) finishPlugins(cause error) error {
	if e.plugins == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(e.context()), 30*time.Second)
	defer cancel()
	status := "completed"
	if cause != nil {
		status = "failed"
	}
	if e.context().Err() != nil {
		status = "cancelled"
	}
	var err error
	if e.pluginExecution != nil {
		e.pluginExecution.mu.Lock()
		nodes := append([]ExecutionNode(nil), e.pluginExecution.record.Nodes...)
		e.pluginExecution.mu.Unlock()
		for _, node := range nodes {
			state := node.Phase
			if state == "pending" {
				state = "not run"
			}
			err = errors.Join(err, e.plugins.Emit(ctx, sdk.Event{Phase: "node.finished", Node: node.Name, Status: state}))
		}
	}
	err = errors.Join(err, e.plugins.Emit(ctx, sdk.Event{Phase: "run.finished", Status: status}))
	err = errors.Join(err, e.plugins.Close(ctx))
	e.plugins = nil
	e.pluginExecution = nil
	if err != nil {
		return WithDiagnostic(err, Diagnostic{Code: "plugin_completion_failed", Category: "plugin", Phase: "completion", Remedy: "inspect execution plugin calls and recover external effects without reapplying infrastructure"})
	}
	return nil
}

func (e *Engine) pluginRuntime(name string) exec.RuntimeHook {
	return func(ctx context.Context, operation string) (context.Context, map[string]string, func(error) error, error) {
		m := e.plugins
		if m == nil {
			return ctx, nil, nil, nil
		}
		if ctx == nil {
			ctx = e.context()
		}
		c, env, err := m.Credentials(ctx, name, e.Graph.Nodes[name].Credentials)
		if len(e.Graph.Nodes[name].Credentials) > 0 {
			c = exec.WithCredentialLifetime(c)
		}
		if err != nil {
			return c, nil, nil, err
		}
		phase, after := "native.operation.before", "native.operation.after"
		switch operation {
		case "init":
			phase, after = "node.init.before", "node.init.after"
		case "output", "state pull":
			phase, after = "node.read.before", "node.read.after"
		}
		// Mutation admission runs before its journal transition; this hook only observes runtime completion for mutation commands.
		mutation := operation == "apply" || operation == "destroy"
		if mutation {
			phase = ""
			after = ""
		}
		if phase != "" {
			if err := m.Emit(c, sdk.Event{Phase: phase, Node: name, Metadata: map[string]string{"native_operation": operation}}); err != nil {
				return c, nil, nil, err
			}
		}
		return c, env, func(runtimeErr error) error {
			if after == "" {
				return nil
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(c), 5*time.Second)
			defer cancel()
			status := "completed"
			if runtimeErr != nil {
				status = "failed"
			}
			if err := m.Emit(cleanup, sdk.Event{Phase: after, Node: name, Status: status, Metadata: map[string]string{"native_operation": operation}}); err != nil {
				m.AddCompletionError(err)
			}
			return nil
		}, nil
	}
}

func (e *Engine) pluginPlan(name string, r *exec.Runner, path, phase string) error {
	if e.plugins == nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("node.%s: reading plan identity: %w", name, err)
	}
	sum := sha256.Sum256(data)
	event := sdk.Event{Phase: phase, Node: name, PlanID: hex.EncodeToString(sum[:])}
	if e.plugins.NeedsPlanDocument() {
		event.Plan, err = r.PlanDocument(path)
		if err != nil {
			return err
		}
	}
	if phase == "node.mutation.admit" {
		if err := e.plugins.Admit(name); err != nil {
			return err
		}
		if err := e.plugins.RecheckPlan(e.context(), event); err != nil {
			return err
		}
	}
	if err := e.plugins.Emit(e.context(), event); err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if sha256.Sum256(current) != sum {
		return fmt.Errorf("node.%s: native plan changed during plugin assessment; create a fresh plan", name)
	}
	return nil
}

func (e *Engine) pluginAdmitNative(name, operation string) error {
	if e.plugins == nil {
		return nil
	}
	if e.plugins.Has("node.plan.ready") {
		return fmt.Errorf("node.%s: configured plugin requires plan evidence but %s has none; use a saved-plan operation", name, operation)
	}
	if err := e.plugins.Admit(name); err != nil {
		return err
	}
	return e.plugins.Emit(e.context(), sdk.Event{Phase: "node.mutation.admit", Node: name, Metadata: map[string]string{"native_operation": operation}})
}

func (s *executionSession) recordPlugin(call sdk.CallRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.record
	next.SchemaVersion = 2
	next.PluginCalls = append([]sdk.CallRecord(nil), next.PluginCalls...)
	found := false
	for i, old := range next.PluginCalls {
		if old.ID == call.ID {
			next.PluginCalls[i] = call
			found = true
			break
		}
	}
	if !found {
		if len(next.PluginCalls) >= 10000 {
			return fmt.Errorf("plugin execution call limit reached; reduce the selected scope")
		}
		next.PluginCalls = append(next.PluginCalls, call)
	}
	return s.publish(next)
}

func pluginRecoveryRequired(record ExecutionRecord) bool {
	return plugins.NeedsRecovery(record.PluginCalls)
}

func pluginOperation(operation string) string {
	if operation == "saved_apply" {
		return "plan"
	}
	return operation
}
