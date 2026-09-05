package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// SortedRelationships canonicalizes and sorts a copy so every renderer and diagnostic stays stable even for a programmatically assembled Graph.
func SortedRelationships(g *Graph) []blueprint.Relationship {
	relationships := append([]blueprint.Relationship(nil), g.Relationships...)
	for i := range relationships {
		if relationships[i].Left > relationships[i].Right {
			relationships[i].Left, relationships[i].Right = relationships[i].Right, relationships[i].Left
		}
	}
	sort.Slice(relationships, func(i, j int) bool {
		if relationships[i].Left != relationships[j].Left {
			return relationships[i].Left < relationships[j].Left
		}
		return relationships[i].Right < relationships[j].Right
	})
	return relationships
}

// RelationshipDOT renders only the undirected architectural overlay so execution dependencies cannot be mistaken for peer relationships.
func RelationshipDOT(g *Graph) string {
	var b strings.Builder
	b.WriteString("graph terragraph_relationships {\n")
	b.WriteString("  rankdir=LR;\n")
	names := make([]string, 0, len(g.Nodes))
	for name := range g.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "  %q;\n", name)
	}
	for _, relationship := range SortedRelationships(g) {
		fmt.Fprintf(&b, "  %q -- %q;\n", relationship.Left, relationship.Right)
	}
	b.WriteString("}\n")
	return b.String()
}

// relationshipContractProblems keeps the overlay contract-aware without inventing port matching: each leaf must at least have a module-source contract to participate.
func relationshipContractProblems(g *Graph) []Problem {
	severity := SeverityWarning
	if g.ContractMode == "enforce" {
		severity = SeverityError
	}
	var problems []Problem
	for _, relationship := range SortedRelationships(g) {
		for _, name := range []string{relationship.Left, relationship.Right} {
			node := g.Nodes[name]
			if node == nil || g.Contracts.Lookup(contractKey(node)) != nil {
				continue
			}
			problems = append(problems, Problem{Severity: severity, Message: fmt.Sprintf("contract.[C010] relationship node.%s -- node.%s: node.%s source %q has no contract; declare a producer or consumer block for that source or remove the relationship", relationship.Left, relationship.Right, name, node.Source)})
		}
	}
	return problems
}
