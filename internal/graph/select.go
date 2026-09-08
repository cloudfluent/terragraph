package graph

import (
	"fmt"
	"sort"
)

// Selection retains inclusion reasons so preview and execution cannot disagree about an expanded target set.
type Selection struct {
	Levels  [][]string
	Reasons map[string]string
}

// Select expands only explicit roots in each requested direction so including prerequisites does not unexpectedly select their unrelated consumers.
func Select(g *Graph, roots []string, dependencies, dependents bool) (Selection, error) {
	reasons := map[string]string{}
	for _, name := range roots {
		if _, ok := g.Nodes[name]; !ok {
			return Selection{}, fmt.Errorf("unknown node %q; select a leaf node from graph output", name)
		}
		reasons[name] = "explicit"
	}
	if len(roots) == 0 {
		for name := range g.Nodes {
			reasons[name] = "all"
		}
	}
	expand := func(adj map[string][]string, reason string) {
		seen := map[string]bool{}
		queue := append([]string(nil), roots...)
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			if reasons[name] == "" {
				reasons[name] = reason
			}
			queue = append(queue, adj[name]...)
		}
	}
	if dependencies {
		expand(g.In, "dependency")
	}
	if dependents {
		expand(g.Out, "dependent")
	}
	// Validate the complete DAG even when a small selection would hide a cycle outside it.
	if _, err := Levels(g); err != nil {
		return Selection{}, err
	}
	sub := &Graph{Nodes: map[string]*Node{}, In: map[string][]string{}, Out: map[string][]string{}}
	for name := range reasons {
		sub.Nodes[name] = g.Nodes[name]
		for _, parent := range g.In[name] {
			if reasons[parent] != "" {
				sub.In[name] = append(sub.In[name], parent)
			}
		}
		for _, child := range g.Out[name] {
			if reasons[child] != "" {
				sub.Out[name] = append(sub.Out[name], child)
			}
		}
	}
	levels, err := Levels(sub)
	return Selection{Levels: levels, Reasons: reasons}, err
}

// OutsideDependents includes transitive consumers because selecting a distant leaf cannot make an omitted intermediate dependency safe to destroy.
func OutsideDependents(g *Graph, selected map[string]string) []string {
	seen := map[string]bool{}
	queue := make([]string, 0, len(selected))
	for name := range selected {
		queue = append(queue, name)
	}
	outside := []string{}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		if selected[name] == "" {
			outside = append(outside, name)
		}
		queue = append(queue, g.Out[name]...)
	}
	sort.Strings(outside)
	return outside
}
