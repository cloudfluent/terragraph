package graph

import "sort"

// FindCycles returns every cyclic strongly-connected component in the graph: every SCC of size > 1, plus any single node with a self-edge. Unlike FindCycle (which stops at the first cycle it meets), this finds all of them in one pass so a blueprint with several independent cyclic clusters gets every one reported at once.
//
// Standard Tarjan's algorithm (index/lowlink/onStack), visiting nodes in sorted order for determinism.
func FindCycles(g *Graph) [][]string {
	var (
		index   = 0
		indices = make(map[string]int, len(g.Nodes))
		lowlink = make(map[string]int, len(g.Nodes))
		onStack = make(map[string]bool, len(g.Nodes))
		stack   []string
		sccs    [][]string
	)

	var strongConnect func(v string)
	strongConnect = func(v string) {
		indices[v] = index
		lowlink[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		next := append([]string(nil), g.Out[v]...)
		sort.Strings(next)
		for _, w := range next {
			if _, seen := indices[w]; !seen {
				strongConnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlink[v] {
					lowlink[v] = indices[w]
				}
			}
		}

		if lowlink[v] == indices[v] {
			var scc []string
			for {
				n := len(stack) - 1
				w := stack[n]
				stack = stack[:n]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			if isCyclic(g, scc) {
				sort.Strings(scc)
				sccs = append(sccs, scc)
			}
		}
	}

	names := make([]string, 0, len(g.Nodes))
	for name := range g.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if _, seen := indices[name]; !seen {
			strongConnect(name)
		}
	}

	sort.Slice(sccs, func(i, j int) bool { return sccs[i][0] < sccs[j][0] })
	return sccs
}

// isCyclic reports whether an SCC actually represents a cycle: more than one node, or a single node with an edge to itself.
func isCyclic(g *Graph, scc []string) bool {
	if len(scc) > 1 {
		return true
	}
	n := scc[0]
	for _, m := range g.Out[n] {
		if m == n {
			return true
		}
	}
	return false
}
