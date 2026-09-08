package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cloudfluent/terragraph/internal/graph"
)

// ConcurrencyPool limits simultaneous users of a shared service without inventing ordering dependencies between them.
type ConcurrencyPool struct {
	Name  string
	Limit int
	Nodes []string
}

// ExecutionPreview describes scheduling prerequisites without reading runtime output or producing a Terraform plan.
type ExecutionPreview struct {
	Operation            string
	Nodes                []SelectedNode
	OutsideDependents    []string
	DestroyScopeComplete bool
}

// SelectedNode explains scope expansion and separates external prerequisites from inputs that will be read from existing state.
type SelectedNode struct {
	Node                  string
	Level                 int
	Reason                string
	Prerequisites         []string
	ExternalPrerequisites []string
	ExternalInputs        []InputBasis
	Pools                 []string
}

func (e *Engine) selection(opts Options) (graph.Selection, error) {
	roots := append([]string(nil), opts.Nodes...)
	if opts.Node != "" {
		roots = append(roots, opts.Node)
	}
	return graph.Select(e.Graph, roots, opts.IncludeDependencies, opts.IncludeDependents)
}

func (e *Engine) validateOptions(opts Options) error {
	if opts.NodeTimeout < 0 || opts.OutputRetries < 0 || opts.OutputRetries > 10 {
		return fmt.Errorf("execution: timeout must be non-negative and output retries between 0 and 10; adjust execution limits")
	}
	timeoutNodes := make([]string, 0, len(opts.Timeouts))
	for name := range opts.Timeouts {
		timeoutNodes = append(timeoutNodes, name)
	}
	sort.Strings(timeoutNodes)
	for _, name := range timeoutNodes {
		timeout := opts.Timeouts[name]
		if _, ok := e.Graph.Nodes[name]; !ok {
			return fmt.Errorf("node.%s.timeout: unknown node; select a leaf node from graph output", name)
		}
		if timeout < 0 {
			return fmt.Errorf("node.%s.timeout: duration must be non-negative", name)
		}
	}
	pools := map[string]bool{}
	for _, pool := range opts.Pools {
		if pool.Name == "" || pools[pool.Name] || pool.Limit < 1 || len(pool.Nodes) == 0 {
			return fmt.Errorf("pool.%s: supply a unique name, positive limit, and at least one node", pool.Name)
		}
		pools[pool.Name] = true
		seen := map[string]bool{}
		for _, name := range pool.Nodes {
			if _, ok := e.Graph.Nodes[name]; !ok || seen[name] {
				return fmt.Errorf("pool.%s: unknown or repeated node %q; list each leaf once", pool.Name, name)
			}
			seen[name] = true
		}
	}
	return nil
}

// Preview shares selection with the scheduler, but never acquires execution locks or starts a runtime.
func (e *Engine) Preview(opts Options, operation string) (ExecutionPreview, error) {
	preview := ExecutionPreview{Operation: operation, Nodes: []SelectedNode{}, OutsideDependents: []string{}, DestroyScopeComplete: true}
	if operation != "plan" && operation != "apply" && operation != "destroy" {
		return preview, fmt.Errorf("execution: unknown operation %q", operation)
	}
	if err := e.validateOptions(opts); err != nil {
		return preview, err
	}
	if opts.Resume {
		var err error
		opts, err = e.resumeOptions(opts, operation)
		if err != nil {
			return preview, err
		}
	}
	selection, err := e.selection(opts)
	if err != nil {
		return preview, err
	}
	levels, err := e.executionLevels(opts, operation == "destroy")
	if err != nil {
		return preview, err
	}
	if operation == "destroy" {
		preview.OutsideDependents = graph.OutsideDependents(e.Graph, selection.Reasons)
		preview.DestroyScopeComplete = len(preview.OutsideDependents) == 0
	}
	prerequisites := e.Graph.In
	if operation == "destroy" {
		prerequisites = e.Graph.Out
	}
	for i, level := range levels {
		for _, name := range level {
			item := SelectedNode{Node: name, Level: i + 1, Reason: selection.Reasons[name], Prerequisites: []string{}, ExternalPrerequisites: []string{}, ExternalInputs: []InputBasis{}, Pools: []string{}}
			for _, parent := range prerequisites[name] {
				if selection.Reasons[parent] != "" {
					item.Prerequisites = append(item.Prerequisites, parent)
				} else {
					item.ExternalPrerequisites = append(item.ExternalPrerequisites, parent)
				}
			}
			for _, edge := range e.Graph.Edges {
				if edge.IsDataEdge() && edge.To.Node == name && selection.Reasons[edge.From.Node] == "" {
					item.ExternalInputs = append(item.ExternalInputs, InputBasis{Input: edge.To.Name, Node: edge.From.Node, Output: edge.From.Name, Source: "existing_state"})
				}
			}
			for _, pool := range opts.Pools {
				for _, member := range pool.Nodes {
					if member == name {
						item.Pools = append(item.Pools, pool.Name)
					}
				}
			}
			sort.Strings(item.Prerequisites)
			sort.Strings(item.ExternalPrerequisites)
			sort.Strings(item.Pools)
			sort.Slice(item.ExternalInputs, func(i, j int) bool { return item.ExternalInputs[i].Input < item.ExternalInputs[j].Input })
			preview.Nodes = append(preview.Nodes, item)
		}
	}
	return preview, nil
}

func (e *Engine) checkDestroyScope(opts Options) error {
	selection, err := e.selection(opts)
	if err != nil {
		return err
	}
	outside := graph.OutsideDependents(e.Graph, selection.Reasons)
	if len(outside) > 0 && !opts.AllowOrphanDestroy {
		return fmt.Errorf("destroy: dependent nodes outside selection: %s; use --include-dependents or explicitly acknowledge with --allow-orphan-destroy", strings.Join(outside, ", "))
	}
	return nil
}

// Timed mutating runs require unattended approval because console readers cannot be cancelled uniformly on every supported platform.
func checkTimedApproval(opts Options) error {
	timed := opts.NodeTimeout > 0
	for _, timeout := range opts.Timeouts {
		timed = timed || timeout > 0
	}
	if timed && !opts.AutoApprove {
		return fmt.Errorf("node timeouts need --auto-approve for apply/destroy: interactive approval cannot be interrupted on every platform; use --auto-approve or remove timeouts")
	}
	return nil
}
