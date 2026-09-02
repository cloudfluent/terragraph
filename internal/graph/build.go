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
	// Runtime is this node's fully resolved runtime declaration (see blueprint.Runtime), already following the blueprint.Node.Runtime -> enclosing blueprint.Use.Runtime cascade (see build). Nil means neither this node nor anything it's nested under ever named a runtime; engine.Engine.runtimeFor applies the remaining CLI/built-in fallback layers on top of that, which are not graph concerns.
	Runtime *blueprint.Runtime
	// Env is this node's fully resolved extra environment variables (see blueprint.Node.Env), already merged with whatever an enclosing blueprint.Use.Env cascade contributed (see build). Nil/empty means nothing anywhere in this node's chain ever set one.
	Env map[string]string
}

// Graph is the blueprint resolved into a DAG: nodes with schema attached, plus adjacency in both directions. Out[a] contains every node that has an edge from a (i.e. must run after a); In[a] contains every node that has an edge to a (i.e. must run before a).
type Graph struct {
	Nodes map[string]*Node
	Edges []blueprint.Edge
	Out   map[string][]string
	In    map[string][]string
}

// cloneNode returns a value copy of n with its BackendConfig/Vars/Env maps deep-copied. n.Nodes for a group instantiation come from a group definition that resolveContext.parseGroupDir may hand back to more than one `use` site (see loadGroupDef); without this, every instance of the same group would share the exact same underlying BackendConfig/Vars/Env map objects, since a plain struct copy only copies the map header, not its contents.
func cloneNode(n blueprint.Node) blueprint.Node {
	if n.BackendConfig != nil {
		clone := make(map[string]string, len(n.BackendConfig))
		for k, v := range n.BackendConfig {
			clone[k] = v
		}
		n.BackendConfig = clone
	}
	if n.Vars != nil {
		clone := make(map[string]any, len(n.Vars))
		for k, v := range n.Vars {
			clone[k] = v
		}
		n.Vars = clone
	}
	if n.Env != nil {
		clone := make(map[string]string, len(n.Env))
		for k, v := range n.Env {
			clone[k] = v
		}
		n.Env = clone
	}
	return n
}

// mergeEnv layers override on top of base, returning a new map with override's entries winning key-by-key (base is never mutated). Returns nil, not an empty map, when the result would have no entries: a nil graph.Node.Env is what tells the engine "nothing at all to add", the same nil-means-absent convention Runtime already uses.
func mergeEnv(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}

// Build resolves a blueprint into a Graph, recursively expanding any group instantiations (`use` blocks). baseDir is the directory the blueprint file lives in and must be absolute: it becomes the root every relative node/group source resolves against, directly or (for a node inside a group) transitively. Build fails fast if a node's source directory cannot be inspected (e.g. it doesn't exist); that is a structural problem, not something validate can usefully report alongside others.
func Build(bp *blueprint.Blueprint, baseDir string) (*Graph, error) {
	g, _, err := build(bp, baseDir, "", nil, nil, &resolveContext{})
	return g, err
}

// build resolves bp into a Graph, also returning this scope's own use instances (keyed by their unqualified `as` name), needed by a caller that is itself resolving a group's Export, since an export mapping may reference either a plain internal node or one of this same group's own use instances (see resolveExportEndpoint). ambient is the runtime, if any, this whole scope inherits from the `use` block that instantiated it (nil at the top level, or when that use set none): it becomes a node's Runtime when the node names no blueprint.Node.Runtime of its own, and it cascades unchanged into a nested use's own recursive build unless that nested use sets its own override (see the loop below). ambientEnv is the analogous inherited environment, except it merges rather than replaces at every layer (see mergeEnv).
func build(bp *blueprint.Blueprint, baseDir, namespace string, ambient *blueprint.Runtime, ambientEnv map[string]string, rc *resolveContext) (*Graph, map[string]useInfo, error) {
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

	// bp.Runtimes was already validated (every blueprint.Node.Runtime/blueprint.Use.Runtime in this same parse scope names an entry here) by blueprint.ParseFile/ParseDir before Build ever sees it, so a lookup miss below can't happen; runtimeFor's ok result exists only to satisfy the map-access form.
	runtimeFor := func(name string) (blueprint.Runtime, bool) {
		for _, rt := range bp.Runtimes {
			if rt.Name == name {
				return rt, true
			}
		}
		return blueprint.Runtime{}, false
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
		schema, err := rc.inspect(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("node %q: %w", n.Name, err)
		}
		qn := cloneNode(n)
		qn.Name = qualify(n.Name)

		resolved := ambient
		if n.Runtime != "" {
			rt, _ := runtimeFor(n.Runtime)
			resolved = &rt
		}
		env := mergeEnv(ambientEnv, n.Env)

		g.Nodes[qn.Name] = &Node{Node: qn, Schema: schema, Dir: dir, Runtime: resolved, Env: env}
	}

	uses := map[string]useInfo{}
	for _, u := range bp.Uses {
		nextAmbient := ambient
		if u.Runtime != "" {
			rt, _ := runtimeFor(u.Runtime)
			nextAmbient = &rt
		}
		nextAmbientEnv := mergeEnv(ambientEnv, u.Env)

		info, internal, err := resolveUse(u, baseDir, qualify(u.As), nextAmbient, nextAmbientEnv, rc)
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

// resolveUse loads and builds the group instantiated by u (recursing through the group's own nested `use` blocks, if any), validates it, and returns both its expanded internal graph (nodes already namespaced under instancePrefix, ready to splice into the caller's graph) and a useInfo describing how the caller's edges should resolve references to this instance. ambient is u's own resolved runtime override, if any (already merged with whatever u itself inherited from further out), the new default every node inside this instance inherits unless it names its own; ambientEnv is the analogous already-merged environment.
func resolveUse(u blueprint.Use, referencingDir, instancePrefix string, ambient *blueprint.Runtime, ambientEnv map[string]string, rc *resolveContext) (useInfo, *Graph, error) {
	groupDir := filepath.Join(referencingDir, u.Source)

	pop, err := rc.push(groupDir, u.GroupName)
	if err != nil {
		return useInfo{}, nil, err
	}
	defer pop()

	def, groupRuntimes, err := loadGroupDef(rc, groupDir, u.GroupName)
	if err != nil {
		return useInfo{}, nil, err
	}

	// def.Nodes/Uses may reference a runtime by name (blueprint.Node.Runtime / blueprint.Use.Runtime); those names resolve against groupRuntimes, the `runtime` blocks declared in this same group source directory, never against whatever the outer scope that wrote u happens to have declared (see blueprint.validateRuntimes, which already enforced this scoping at parse time).
	innerBP := &blueprint.Blueprint{Nodes: def.Nodes, Edges: def.Edges, Uses: def.Uses, Runtimes: groupRuntimes}
	internal, innerUses, err := build(innerBP, groupDir, instancePrefix, ambient, ambientEnv, rc)
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
