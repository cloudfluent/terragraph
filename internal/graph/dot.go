package graph

import (
	"fmt"
	"sort"
	"strings"
)

// DOT renders the graph in Graphviz format, as a stopgap visualization until the Web UI node editor exists. Data edges (carrying a value) are solid and labeled with the port names; ordering-only edges are dashed.
func DOT(g *Graph) string {
	var b strings.Builder
	b.WriteString("digraph terragraph {\n")
	b.WriteString("  rankdir=LR;\n")

	names := make([]string, 0, len(g.Nodes))
	for name := range g.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "  %q;\n", name)
	}

	for _, e := range g.Edges {
		if e.IsDataEdge() {
			fmt.Fprintf(&b, "  %q -> %q [label=%q];\n", e.From.Node, e.To.Node, e.From.Name+" -> "+e.To.Name)
		} else {
			fmt.Fprintf(&b, "  %q -> %q [style=dashed];\n", e.From.Node, e.To.Node)
		}
	}

	b.WriteString("}\n")
	return b.String()
}
