// Package graph turns a parsed blueprint into an executable DAG: each node carries its real Terraform/OpenTofu variable/output schema, and edges are resolved into adjacency lists for validation and topological execution order. Group instantiations (`use` blocks) are expanded here too: their internal nodes are spliced in under a dotted namespace, and edges referencing the instance are rewritten to their real internal targets. By the time Build returns, every edge is a plain node-to-node edge.
package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

// Node is a blueprint node enriched with its module's real variable/output schema, read directly from its .tf files.
type Node struct {
	blueprint.Node
	Schema *module.Schema
	// Dir is the resolved, absolute directory this node's Terraform files live in. For a node expanded from a group instance, this is relative to the group definition's own directory, not the outer blueprint's, so callers must use Dir directly rather than re-joining Node.Source against the outer blueprint directory.
	Dir string
}

// Graph is the blueprint resolved into a DAG: nodes with schema attached, plus adjacency in both directions. Out[a] contains every node that has an edge from a (i.e. must run after a); In[a] contains every node that has an edge to a (i.e. must run before a).
type Graph struct {
	Nodes map[string]*Node
	Edges []blueprint.Edge
	Out   map[string][]string
	In    map[string][]string
}

// Build resolves a blueprint into a Graph, recursively expanding any group instantiations (`use` blocks). baseDir is the directory the blueprint file lives in and must be absolute: it becomes the root every relative node/group source resolves against, directly or (for a node inside a group) transitively. Build fails fast if a node's source directory cannot be inspected (e.g. it doesn't exist); that is a structural problem, not something validate can usefully report alongside others.
func Build(bp *blueprint.Blueprint, baseDir string) (*Graph, error) {
	g, _, err := build(bp, baseDir, "", &resolveContext{})
	return g, err
}

// build resolves bp into a Graph, also returning this scope's own use instances (keyed by their unqualified `as` name), needed by a caller that is itself resolving a group's Export, since an export mapping may reference either a plain internal node or one of this same group's own use instances (see resolveExportEndpoint).
func build(bp *blueprint.Blueprint, baseDir, namespace string, rc *resolveContext) (*Graph, map[string]useInfo, error) {
	g := &Graph{
		Nodes: make(map[string]*Node),
		Out:   make(map[string][]string),
		In:    make(map[string][]string),
	}

	qualify := func(name string) string {
		if namespace == "" {
			return name
		}
		return namespace + "." + name
	}

	for _, n := range bp.Nodes {
		dir := filepath.Join(baseDir, n.Source)
		if blueprint.IsRemote(n.Source) {
			dir = filepath.Join(baseDir, bp.VendorDirectory(), n.Name)
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				return nil, nil, fmt.Errorf(
					"node %q: source %q is not vendored yet; run \"terragraph vendor --node %s\"",
					n.Name, n.Source, n.Name,
				)
			}
		}
		schema, err := module.Inspect(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("node %q: %w", n.Name, err)
		}
		qn := n
		qn.Name = qualify(n.Name)
		g.Nodes[qn.Name] = &Node{Node: qn, Schema: schema, Dir: dir}
	}

	uses := map[string]useInfo{}
	for _, u := range bp.Uses {
		info, internal, err := resolveUse(u, baseDir, qualify(u.As), rc)
		if err != nil {
			return nil, nil, fmt.Errorf("use %q as %q: %w", u.GroupName, u.As, err)
		}
		uses[u.As] = info

		for name, node := range internal.Nodes {
			g.Nodes[name] = node
		}
		for _, e := range internal.Edges {
			g.Edges = append(g.Edges, e)
			g.Out[e.From.Node] = append(g.Out[e.From.Node], e.To.Node)
			g.In[e.To.Node] = append(g.In[e.To.Node], e.From.Node)
		}
	}

	for _, e := range bp.Edges {
		rewritten, err := rewriteEdge(e, uses, qualify)
		if err != nil {
			return nil, nil, err
		}
		for _, re := range rewritten {
			g.Edges = append(g.Edges, re)
			g.Out[re.From.Node] = append(g.Out[re.From.Node], re.To.Node)
			g.In[re.To.Node] = append(g.In[re.To.Node], re.From.Node)
		}
	}

	return g, uses, nil
}

// resolveUse loads and builds the group instantiated by u (recursing through the group's own nested `use` blocks, if any), validates it, and returns both its expanded internal graph (nodes already namespaced under instancePrefix, ready to splice into the caller's graph) and a useInfo describing how the caller's edges should resolve references to this instance.
func resolveUse(u blueprint.Use, referencingDir, instancePrefix string, rc *resolveContext) (useInfo, *Graph, error) {
	groupDir := filepath.Join(referencingDir, u.Source)

	pop, err := rc.push(groupDir, u.GroupName)
	if err != nil {
		return useInfo{}, nil, err
	}
	defer pop()

	def, err := loadGroupDef(groupDir, u.GroupName)
	if err != nil {
		return useInfo{}, nil, err
	}

	innerBP := &blueprint.Blueprint{Nodes: def.Nodes, Edges: def.Edges, Uses: def.Uses}
	internal, innerUses, err := build(innerBP, groupDir, instancePrefix, rc)
	if err != nil {
		return useInfo{}, nil, err
	}

	for _, p := range Validate(internal) {
		if p.IsError() {
			return useInfo{}, nil, fmt.Errorf("group is invalid: %s", p.Message)
		}
	}

	// def.Export's own references may point at a plain internal node or at one of this group's own use instances (innerUses). Resolve through either, down to real leaf ports, exactly as an edge endpoint would be.
	qualify := func(name string) string { return instancePrefix + "." + name }
	resolvedExport, err := resolveExport(def.Export, innerUses, qualify)
	if err != nil {
		return useInfo{}, nil, err
	}

	if _, err := synthesizeSchema(resolvedExport, internal); err != nil {
		return useInfo{}, nil, err
	}

	var roots, sinks []string
	for name := range internal.Nodes {
		if len(internal.In[name]) == 0 {
			roots = append(roots, name)
		}
		if len(internal.Out[name]) == 0 {
			sinks = append(sinks, name)
		}
	}
	sort.Strings(roots)
	sort.Strings(sinks)

	return useInfo{
		export: resolvedExport,
		roots:  roots,
		sinks:  sinks,
	}, internal, nil
}
