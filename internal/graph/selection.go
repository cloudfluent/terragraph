package graph

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// Selection preserves why leaves were selected without retaining input values or changing the source graph.
type Selection struct {
	SchemaVersion int              `json:"schema_version"`
	Mode          string           `json:"mode"`
	Requested     []string         `json:"requested"`
	Nodes         []SelectionNode  `json:"nodes"`
	BoundaryEdges []blueprint.Edge `json:"boundary_edges"`
}

// SelectionNode records immediate predecessors instead of exponentially many reachability paths.
type SelectionNode struct {
	Node   string   `json:"node"`
	Reason string   `json:"reason"`
	Via    []string `json:"via"`
}

// Select resolves exact leaf names; nil requests mean the default whole graph, while an empty non-nil request is invalid.
func Select(g *Graph, requested []string, downstream bool) (*Selection, error) {
	if requested == nil && !downstream {
		return nil, nil
	}
	if len(requested) == 0 {
		return nil, fmt.Errorf("selection: specify at least one --node <leaf> with --downstream")
	}
	seeds := append([]string{}, requested...)
	sort.Strings(seeds)
	seeds = slices.Compact(seeds)
	selected := map[string]bool{}
	requestedSet := map[string]bool{}
	for _, name := range seeds {
		if name == "" {
			return nil, fmt.Errorf("selection: empty --node; specify an exact leaf name")
		}
		if g.Nodes[name] == nil {
			examples := []string{}
			for leaf := range g.Nodes {
				if strings.HasPrefix(leaf, name+".") {
					examples = append(examples, leaf)
				}
			}
			sort.Strings(examples)
			if len(examples) > 0 {
				return nil, fmt.Errorf("node.%s: select a qualified leaf such as --node %s; group selection is not supported", name, examples[0])
			}
			return nil, fmt.Errorf("unknown node %q; use an exact leaf name and repeat --node for multiple nodes (commas are not separators)", name)
		}
		selected[name] = true
		requestedSet[name] = true
	}
	if downstream {
		queue := append([]string{}, seeds...)
		for i := 0; i < len(queue); i++ {
			for _, name := range g.Out[queue[i]] {
				if !selected[name] {
					selected[name] = true
					queue = append(queue, name)
				}
			}
		}
	}
	s := &Selection{SchemaVersion: 1, Mode: "exact", Requested: seeds, Nodes: []SelectionNode{}, BoundaryEdges: []blueprint.Edge{}}
	if downstream {
		s.Mode = "downstream"
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		n := SelectionNode{Node: name, Reason: "requested", Via: []string{}}
		if !requestedSet[name] {
			n.Reason = "downstream"
			for _, parent := range g.In[name] {
				if selected[parent] {
					n.Via = append(n.Via, parent)
				}
			}
			sort.Strings(n.Via)
			n.Via = slices.Compact(n.Via)
		}
		s.Nodes = append(s.Nodes, n)
	}
	for _, edge := range g.Edges {
		if selected[edge.From.Node] != selected[edge.To.Node] {
			s.BoundaryEdges = append(s.BoundaryEdges, edge)
		}
	}
	sort.Slice(s.BoundaryEdges, func(i, j int) bool {
		return compareBoundaryEdges(s.BoundaryEdges[i], s.BoundaryEdges[j]) < 0
	})
	return s, nil
}

func compareBoundaryEdges(a, b blueprint.Edge) int {
	return slices.Compare([]string{a.From.Node, a.From.Name, a.To.Node, a.To.Name, edgeKind(a)}, []string{b.From.Node, b.From.Name, b.To.Node, b.To.Name, edgeKind(b)})
}

func edgeKind(e blueprint.Edge) string {
	if e.IsDataEdge() {
		return "data"
	}
	return "ordering"
}

// SelectedLevels filters the original schedule, preserving full-graph ordering even when omitted predecessors leave gaps.
func SelectedLevels(g *Graph, names []string, reverse bool) ([][]string, error) {
	levels, err := Levels(g)
	if err != nil {
		return nil, err
	}
	if names == nil {
		if reverse {
			slices.Reverse(levels)
		}
		return levels, nil
	}
	selected := map[string]bool{}
	for _, name := range names {
		if g.Nodes[name] == nil {
			return nil, fmt.Errorf("node.%s: removed from graph; create a fresh plan", name)
		}
		selected[name] = true
	}
	out := [][]string{}
	for _, level := range levels {
		kept := []string{}
		for _, name := range level {
			if selected[name] {
				kept = append(kept, name)
			}
		}
		if len(kept) > 0 {
			out = append(out, kept)
		}
	}
	if reverse {
		slices.Reverse(out)
	}
	return out, nil
}

// Names returns an explicit membership list so an empty resolved selection cannot become a whole-graph run.
func (s *Selection) Names() []string {
	names := make([]string, 0, len(s.Nodes))
	for _, n := range s.Nodes {
		names = append(names, n.Node)
	}
	return names
}

// ValidateMembership rejects corrupt stored metadata without using it to widen authoritative record membership.
func (s *Selection) ValidateMembership(names []string) error {
	invalid := func() error {
		return fmt.Errorf("execution selection is incompatible or inconsistent with recorded nodes; restore a valid record")
	}
	if s.SchemaVersion != 1 || (s.Mode != "exact" && s.Mode != "downstream") || len(s.Requested) == 0 || s.Nodes == nil || s.BoundaryEdges == nil {
		return invalid()
	}
	members := map[string]bool{}
	for _, name := range names {
		if members[name] || name == "" {
			return invalid()
		}
		members[name] = true
	}
	if len(s.Nodes) != len(members) || !slices.IsSorted(s.Requested) {
		return invalid()
	}
	seeds := map[string]bool{}
	for _, name := range s.Requested {
		if !members[name] || seeds[name] {
			return invalid()
		}
		seeds[name] = true
	}
	seen := map[string]bool{}
	for i, n := range s.Nodes {
		if !members[n.Node] || seen[n.Node] || n.Via == nil || (i > 0 && s.Nodes[i-1].Node >= n.Node) {
			return invalid()
		}
		seen[n.Node] = true
		if seeds[n.Node] {
			if n.Reason != "requested" || len(n.Via) != 0 {
				return invalid()
			}
		} else {
			if s.Mode != "downstream" || n.Reason != "downstream" || len(n.Via) == 0 || !slices.IsSorted(n.Via) {
				return invalid()
			}
			for j, p := range n.Via {
				if !members[p] || p == n.Node || (j > 0 && n.Via[j-1] == p) {
					return invalid()
				}
			}
		}
	}
	reachable := map[string]bool{}
	for name := range seeds {
		reachable[name] = true
	}
	for changed := true; changed; {
		changed = false
		for _, n := range s.Nodes {
			for _, p := range n.Via {
				if reachable[p] && !reachable[n.Node] {
					reachable[n.Node] = true
					changed = true
				}
			}
		}
	}
	if len(reachable) != len(members) {
		return invalid()
	}
	for i, edge := range s.BoundaryEdges {
		if i > 0 && compareBoundaryEdges(s.BoundaryEdges[i-1], edge) > 0 {
			return invalid()
		}
		if edge.From.Node == "" || edge.To.Node == "" || members[edge.From.Node] == members[edge.To.Node] {
			return invalid()
		}
		if edge.IsDataEdge() {
			if edge.From.Kind != blueprint.PortOutput || edge.To.Kind != blueprint.PortInput || edge.From.Name == "" || edge.To.Name == "" {
				return invalid()
			}
		} else if edge.From.IsPort() || edge.To.IsPort() || edge.From.Name != "" || edge.To.Name != "" {
			return invalid()
		}
	}
	return nil
}
