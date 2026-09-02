package graph

import (
	"fmt"
	"sort"
	"strings"
)

// Levels groups nodes into execution layers: every node in layer i only depends on nodes in layers < i, so nodes within one layer have no edge between them and are safe to run concurrently (this is what makes parallel plan/apply within a layer sound). Ties within a layer are broken alphabetically so the grouping is stable across runs. Returns an error if the graph has a cycle.
func Levels(g *Graph) ([][]string, error) {
	inDegree := make(map[string]int, len(g.Nodes))
	for name := range g.Nodes {
		inDegree[name] = len(g.In[name])
	}

	var levels [][]string
	placed := 0
	for {
		var level []string
		for name, d := range inDegree {
			if d == 0 {
				level = append(level, name)
			}
		}
		if len(level) == 0 {
			break
		}
		sort.Strings(level)
		levels = append(levels, level)
		placed += len(level)

		for _, n := range level {
			delete(inDegree, n)
			next := append([]string(nil), g.Out[n]...)
			for _, m := range next {
				if _, ok := inDegree[m]; ok {
					inDegree[m]--
				}
			}
		}
	}

	if placed != len(g.Nodes) {
		cycle := FindCycle(g)
		return nil, fmt.Errorf("blueprint graph has a cycle involving nodes: %s", strings.Join(cycle, ", "))
	}
	return levels, nil
}

// TopoSort returns node names in a valid execution order (every node comes after everything it depends on) by flattening Levels. Ties are broken alphabetically so the order is stable across runs. Returns an error if the graph has a cycle.
func TopoSort(g *Graph) ([]string, error) {
	levels, err := Levels(g)
	if err != nil {
		return nil, err
	}
	order := make([]string, 0, len(g.Nodes))
	for _, level := range levels {
		order = append(order, level...)
	}
	return order, nil
}
