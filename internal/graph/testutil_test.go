package graph

import (
	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

// newGraph builds a Graph directly from node names and edges, without touching disk. Build() itself is exercised separately by the examples/basic end-to-end check, so these algorithm-focused tests skip module inspection entirely.
func newGraph(names []string, edges []blueprint.Edge) *Graph {
	g := &Graph{
		Nodes: make(map[string]*Node, len(names)),
		Out:   make(map[string][]string, len(names)),
		In:    make(map[string][]string, len(names)),
	}
	for _, n := range names {
		g.Nodes[n] = &Node{
			Node:   blueprint.Node{Name: n},
			Schema: &module.Schema{Variables: map[string]module.Variable{}, Outputs: map[string]bool{}},
		}
	}
	g.Edges = edges
	for _, e := range edges {
		g.Out[e.From.Node] = append(g.Out[e.From.Node], e.To.Node)
		g.In[e.To.Node] = append(g.In[e.To.Node], e.From.Node)
	}
	return g
}

func dataEdge(fromNode, fromOutput, toNode, toInput string) blueprint.Edge {
	return blueprint.Edge{
		From: blueprint.PortRef{Node: fromNode, Kind: blueprint.PortOutput, Name: fromOutput},
		To:   blueprint.PortRef{Node: toNode, Kind: blueprint.PortInput, Name: toInput},
	}
}

func orderEdge(fromNode, toNode string) blueprint.Edge {
	return blueprint.Edge{
		From: blueprint.PortRef{Node: fromNode},
		To:   blueprint.PortRef{Node: toNode},
	}
}
