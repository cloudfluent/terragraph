package plugins

import (
	"context"
	"fmt"
	"regexp"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	sdk "github.com/cloudfluent/terragraph/plugin"
)

// Expand returns a fresh declaration set so a rejected expansion never leaves a partially modified blueprint.
func (m *Lifecycle) Expand(ctx context.Context, bp *blueprint.Blueprint) error {
	if m == nil {
		return nil
	}
	nodes := append([]blueprint.Node(nil), bp.Nodes...)
	edges := append([]blueprint.Edge(nil), bp.Edges...)
	seen := map[string]bool{}
	for _, n := range nodes {
		seen[n.Name] = true
	}
	for _, u := range bp.Uses {
		seen[u.As] = true
	}
	for _, f := range m.features {
		if f.feature.Kind != "expansion" {
			continue
		}
		response, err := m.invoke(ctx, f, sdk.Request{Action: "expand", Config: f.config.Config, Event: sdk.Event{Phase: "config.expand"}})
		if err != nil {
			return err
		}
		if response.Expansion == nil {
			return fmt.Errorf("plugin.%s.%s: expansion returned no declarations", f.config.Name, f.feature.Name)
		}
		for _, n := range response.Expansion.Nodes {
			if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`).MatchString(n.Name) || seen[n.Name] || n.Source == "" {
				return fmt.Errorf("plugin.%s.%s: invalid or conflicting expanded node %q; use stable unique names and explicit sources", f.config.Name, f.feature.Name, n.Name)
			}
			seen[n.Name] = true
			node := blueprint.Node{Name: n.Name, Source: n.Source, Vars: map[string]any{}}
			for key, value := range n.Vars {
				if value.Sensitive {
					return fmt.Errorf("plugin.%s.%s: expansion cannot return secrets; use input bindings", f.config.Name, f.feature.Name)
				}
				v, err := value.Decode()
				if err != nil {
					return err
				}
				node.Vars[key] = v
			}
			nodes = append(nodes, node)
		}
		for _, edge := range response.Expansion.Edges {
			if (edge.Input == "") != (edge.Output == "") {
				return fmt.Errorf("plugin.%s.%s: expanded data edges require both ports", f.config.Name, f.feature.Name)
			}
			a, b := blueprint.PortRef{Node: edge.From, Name: edge.Output}, blueprint.PortRef{Node: edge.To, Name: edge.Input}
			if edge.Input != "" {
				a.Kind = blueprint.PortOutput
				b.Kind = blueprint.PortInput
			}
			edges = append(edges, blueprint.Edge{From: a, To: b})
		}
	}
	bp.Nodes, bp.Edges = nodes, edges
	return m.Emit(ctx, sdk.Event{Phase: "config.expand", Status: "expanded"})
}
