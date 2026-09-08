package engine

import (
	"fmt"
	"sort"
)

// checkRuntimeFiles covers selected nodes and their direct data sources because a --node run can consume an upstream snapshot without executing that upstream.
func (e *Engine) checkRuntimeFiles(opts Options) error {
	levels, err := e.executionLevels(opts, false)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, level := range levels {
		for _, name := range level {
			selected[name] = true
		}
	}
	required := map[string]bool{}
	for name := range selected {
		required[name] = true
	}
	for _, edge := range e.Graph.Edges {
		if edge.IsDataEdge() && selected[edge.To.Node] {
			required[edge.From.Node] = true
		}
	}
	names := make([]string, 0, len(required))
	for name := range required {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		node := e.Graph.Nodes[name]
		if node.Schema == nil || !node.Schema.RequiresTofuFiles {
			continue
		}
		if err := e.runner(name).RequireTofuFiles(); err != nil {
			return fmt.Errorf("node.%s.runtime: %w", name, err)
		}
	}
	return nil
}
