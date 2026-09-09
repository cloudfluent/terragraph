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

// SelectionDOT includes boundary context without mutating the graph used by input resolution and validation.
func SelectionDOT(g *Graph, selection *Selection) string {
	if selection == nil {
		return DOT(g)
	}
	selected := map[string]bool{}
	visible := map[string]bool{}
	for _, n := range selection.Nodes {
		selected[n.Node] = true
		visible[n.Node] = true
	}
	for _, e := range selection.BoundaryEdges {
		visible[e.From.Node] = true
		visible[e.To.Node] = true
	}
	names := make([]string, 0, len(visible))
	for name := range visible {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("digraph terragraph {\n  rankdir=LR;\n  label=\"selected: black; not selected: gray (context only)\";\n")
	for _, name := range names {
		if selected[name] {
			fmt.Fprintf(&b, "  %q [color=black];\n", name)
		} else {
			fmt.Fprintf(&b, "  %q [color=gray, fontcolor=gray, label=%q];\n", name, name+" (not selected)")
		}
	}
	for _, e := range g.Edges {
		if !selected[e.From.Node] && !selected[e.To.Node] {
			continue
		}
		style, label := "solid", e.From.Name+" -> "+e.To.Name
		if !e.IsDataEdge() {
			style, label = "dashed", "ordering"
		}
		color := "black"
		if selected[e.From.Node] != selected[e.To.Node] {
			color = "gray"
			label += " (not selected boundary)"
		}
		fmt.Fprintf(&b, "  %q -> %q [style=%s, color=%s, label=%q];\n", e.From.Node, e.To.Node, style, color, label)
	}
	b.WriteString("}\n")
	return b.String()
}
