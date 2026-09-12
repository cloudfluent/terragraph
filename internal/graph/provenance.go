package graph

import (
	"sort"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// InstanceStep is one group-instantiation level of a node's Prov.Path, recorded while expansion recurses so inspection never has to reconstruct nesting from dotted names. Instance is the step's qualified namespace prefix ("checkout", then "checkout.inner"), so the last prefix joined with the original node name reproduces the expanded leaf name; Group is the declared group name, and Use/GroupDecl point at the use block that instantiated this level and the group block it named, usually in two different files.
type InstanceStep struct {
	Instance  string
	Group     string
	Use       blueprint.Loc
	GroupDecl blueprint.Loc
}

// MappingStep is one export hop a supply or edge traversed during group expansion, ordered original declaration -> final leaf. Via names the traversed export port as written at that level (e.g. "use.checkout.input.cluster_name"); Decl is the declaration to edit in the group file that owns that export.
type MappingStep struct {
	Via  string
	Decl blueprint.Loc
}

// VarSource explains one key present in a node's Vars after expansion: whether the node block declared it itself ("node_vars") or a use.vars landed it here through export mappings ("use_vars"), and, for the latter, every hop taken. Subject names the declaring block as written at its own declaration site (unqualified names), so Subject plus Decl identifies one line even when the same group body backs several instances; an explicit null value is as much a supply as any other.
type VarSource struct {
	From        string
	Subject     string
	Decl        blueprint.Loc
	MappingPath []MappingStep
}

// SettingSource attributes an effective runtime/approve setting to the scope that actually set it: the node block ("node"), the enclosing use whose value survived the cascade ("use"), or nothing anywhere in the blueprint (""). For From "use", Decl pins the attribute inside the use block while UseName/UseDecl locate the block itself. A Decl pointing at a declaration with no effect would mislead, so nested uses attribute to the nearest setter — the one whose value the node carries.
type SettingSource struct {
	From    string
	Decl    blueprint.Loc
	UseName string
	UseDecl blueprint.Loc
}

// NodeProvenance carries where an expanded node came from: the original (unqualified) node block declaration and the instantiation chain that produced this copy. A plain top-level node has a nil Path and just its own Decl.
type NodeProvenance struct {
	Decl blueprint.Loc
	Path []InstanceStep
}

// Edge is a blueprint edge enriched with the export-mapping provenance its rewrite traversed: nil MappingPath means a plain node-to-node connection no export touched. blueprint.Edge is embedded so From/To/Loc/IsDataEdge consumers keep working unchanged.
type Edge struct {
	blueprint.Edge
	// MappingPath lists each export hop in root-to-leaf order; on an edge that traversed exports on both sides, From-side hops precede To-side hops, matching the endpoint order. Each fanned-out copy carries its own path, so fan-out stays visible per destination.
	MappingPath []MappingStep
}

// Relationships lists a node's neighbors over the full graph, data and ordering edges alike: direct (DependsOn/Dependents from In/Out) and transitive (Ancestors/Descendants). Lists are deduped, sorted, and never contain the node itself; traversal is visited-set based, so a node on a cycle terminates its own walk and is merely absent from its own reach — the reach itself says "statically declared review candidates", never predicted changes.
type Relationships struct {
	DependsOn   []string
	Dependents  []string
	Ancestors   []string
	Descendants []string
}

// RelationshipsFor derives a node's four relationship lists from the graph's existing In/Out adjacency. Lists are non-nil (an empty list is a definite answer, not unknown) and a name absent from the graph yields all-empty lists; selecting unknown leaves is the caller's diagnostic to make.
func (g *Graph) RelationshipsFor(name string) Relationships {
	return Relationships{
		DependsOn:   neighbors(g.In[name], name),
		Dependents:  neighbors(g.Out[name], name),
		Ancestors:   reach(g.In, name),
		Descendants: reach(g.Out, name),
	}
}

// neighbors dedupes one adjacency list, drops self-references, and sorts; In/Out carry one entry per edge, so two edges between one pair would otherwise list the neighbor twice.
func neighbors(adj []string, self string) []string {
	seen := map[string]bool{self: true}
	out := []string{}
	for _, n := range adj {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// reach collects every node transitively reachable through adj, excluding self: seeding the visited set with self both terminates cycles and keeps a node on one out of its own ancestors/descendants, while paths that pass back through self need no re-walk because self's own adjacency seeded the queue.
func reach(adj map[string][]string, self string) []string {
	seen := map[string]bool{self: true}
	out := []string{}
	queue := append([]string{}, adj[self]...)
	for i := 0; i < len(queue); i++ {
		n := queue[i]
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		queue = append(queue, adj[n]...)
	}
	sort.Strings(out)
	return out
}

// InputSource classifies how one declared module variable of a node is supplied (#94 §3.2): "edge" (a data edge targeting it), "node_vars"/"use_vars" (the node's own or a propagated use.vars key), "plugin_input" (an explicit plugin input binding), "external_or_default" (no blueprint supply and the module declares a default), "external_required" (neither supply nor default — external supply must be checked, not proven missing), or "conflict" (multiple blueprint supplies target one slot, with Candidates carrying each individual supply). external_* kinds assert nothing about TF_VAR_*, tfvars files, or runtime precedence.
type InputSource struct {
	Kind string
	// Subject names the original declaration ("node.<from>.output.<port>" for edges, "node.<n>.vars.<k>", "use.<as>.vars.<k>", "node.<n>.input.<k>" for plugin bindings); empty for the external kinds, which have no declaration to name.
	Subject     string
	Decl        blueprint.Loc
	MappingPath []MappingStep
	Candidates  []InputSource
}

// InputSourcesFor classifies every variable the node's module Schema declares, keyed by variable name. It reads only what Build already produced — edges, VarSources, plugin bindings, and the schema's HasDefault — never a second parse. The conflict rule mirrors Validate's input_conflict exactly (two data edges, edge + vars, plugin binding + either), so inspection and validation can never disagree about which inputs are oversupplied; supplies targeting a variable the module does not declare are Validate's missing_input problem, not a classification, and stay out of this key set.
func (g *Graph) InputSourcesFor(name string) map[string]InputSource {
	out := map[string]InputSource{}
	node := g.Nodes[name]
	if node == nil || node.Schema == nil {
		return out
	}
	for varName, v := range node.Schema.Variables {
		var supplies []InputSource
		for _, e := range g.Edges {
			if e.IsDataEdge() && e.To.Node == name && e.To.Name == varName {
				supplies = append(supplies, InputSource{
					Kind:        "edge",
					Subject:     "node." + e.From.Node + ".output." + e.From.Name,
					Decl:        e.Loc,
					MappingPath: append([]MappingStep(nil), e.MappingPath...),
				})
			}
		}
		if vs, ok := node.VarSources[varName]; ok {
			supplies = append(supplies, InputSource{Kind: vs.From, Subject: vs.Subject, Decl: vs.Decl, MappingPath: append([]MappingStep(nil), vs.MappingPath...)})
		}
		if _, bound := node.Inputs[varName]; bound {
			supplies = append(supplies, InputSource{Kind: "plugin_input", Subject: "node." + name + ".input." + varName})
		}
		switch {
		case len(supplies) > 1:
			out[varName] = InputSource{Kind: "conflict", Candidates: supplies}
		case len(supplies) == 1:
			out[varName] = supplies[0]
		case v.HasDefault:
			out[varName] = InputSource{Kind: "external_or_default"}
		default:
			out[varName] = InputSource{Kind: "external_required"}
		}
	}
	return out
}
