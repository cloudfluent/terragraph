package cli

import (
	"fmt"
	"io"

	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/graph"
	"github.com/spf13/cobra"
)

// selectionDTO keeps the public scope contract independent from protected execution record encoding.
type selectionDTO struct {
	SchemaVersion int                `json:"schema_version"`
	Mode          string             `json:"mode"`
	Requested     []string           `json:"requested"`
	Nodes         []selectionNodeDTO `json:"nodes"`
	BoundaryEdges []selectionEdgeDTO `json:"boundary_edges"`
}
type selectionNodeDTO struct {
	Node   string   `json:"node"`
	Reason string   `json:"reason"`
	Via    []string `json:"via"`
}
type selectionEdgeDTO struct {
	Kind string           `json:"kind"`
	From selectionPortDTO `json:"from"`
	To   selectionPortDTO `json:"to"`
}
type selectionPortDTO struct {
	Node   string `json:"node"`
	Output string `json:"output,omitempty"`
	Input  string `json:"input,omitempty"`
}

func selectionToDTO(s *graph.Selection) *selectionDTO {
	if s == nil {
		return nil
	}
	dto := &selectionDTO{SchemaVersion: s.SchemaVersion, Mode: s.Mode, Requested: append([]string{}, s.Requested...), Nodes: []selectionNodeDTO{}, BoundaryEdges: []selectionEdgeDTO{}}
	for _, n := range s.Nodes {
		dto.Nodes = append(dto.Nodes, selectionNodeDTO{Node: n.Node, Reason: n.Reason, Via: append([]string{}, n.Via...)})
	}
	for _, e := range s.BoundaryEdges {
		edge := selectionEdgeDTO{Kind: "ordering", From: selectionPortDTO{Node: e.From.Node}, To: selectionPortDTO{Node: e.To.Node}}
		if e.IsDataEdge() {
			edge.Kind = "data"
			edge.From.Output = e.From.Name
			edge.To.Input = e.To.Name
		}
		dto.BoundaryEdges = append(dto.BoundaryEdges, edge)
	}
	return dto
}

func printSelection(w io.Writer, s *selectionDTO, levels [][]string) {
	if s == nil {
		return
	}
	names := []string{}
	for _, n := range s.Nodes {
		names = append(names, n.Node)
	}
	_, _ = fmt.Fprintf(w, "selection: %s\nrequested: %s\nselected: %s\n", s.Mode, joinNames(s.Requested), joinNames(names))
	for _, n := range s.Nodes {
		why := n.Reason
		if why == "downstream" || why == "upstream" {
			why += " of " + joinNames(n.Via)
		}
		_, _ = fmt.Fprintf(w, "  %s: %s\n", n.Node, why)
	}
	if len(s.BoundaryEdges) > 0 {
		_, _ = fmt.Fprintln(w, "boundary edges (one endpoint not selected):")
		for _, e := range s.BoundaryEdges {
			from, to := e.From.Node, e.To.Node
			if e.Kind == "data" {
				from += ".output." + e.From.Output
				to += ".input." + e.To.Input
			}
			_, _ = fmt.Fprintf(w, "  %s -> %s (%s)\n", from, to, e.Kind)
		}
	}
	for i, level := range levels {
		_, _ = fmt.Fprintf(w, "level %d: %s\n", i+1, joinNames(level))
	}
}

type selectionFlags struct {
	nodes      []string
	downstream bool
	upstream   bool
}

func (f *selectionFlags) add(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&f.upstream, "upstream", false, "include all predecessors of --node across data and ordering edges (exclusive with --downstream)")
	cmd.Flags().StringArrayVar(&f.nodes, "node", nil, "select an exact leaf name (repeat for multiple nodes; commas are literal)")
	cmd.Flags().BoolVar(&f.downstream, "downstream", false, "include all successors of --node across data and ordering edges")
}

func (f *selectionFlags) specified(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("node") || cmd.Flags().Changed("downstream") || cmd.Flags().Changed("upstream")
}

func (f *selectionFlags) options(cmd *cobra.Command, format string, result **selectionDTO) engine.Options {
	return engine.Options{Nodes: f.nodes, Downstream: f.downstream, Upstream: f.upstream, SelectionSpecified: f.specified(cmd), OnSelection: func(s *graph.Selection, levels [][]string) {
		*result = selectionToDTO(s)
		if format == "text" {
			printSelection(cmd.OutOrStdout(), *result, levels)
		}
	}}
}
