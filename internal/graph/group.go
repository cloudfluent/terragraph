package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

// resolveContext tracks state for one Build() call: in-progress group resolutions (by absolute source directory + group name), to catch group self-reference cycles, plus per-directory caches so a group source directory or a node's module directory is only ever read and parsed once no matter how many times it's referenced (multiple `use` instances of the same group, or multiple nodes sharing one `source` via backend_config). Both caches are safe uncontended: Build runs entirely single-threaded, and every goroutine terragraph ever spawns (see engine.runLevels) starts only after the graph it walks has already been fully built.
type resolveContext struct {
	stack     []string
	groupDirs map[string]*blueprint.Blueprint
	schemas   map[string]*module.Schema
	// rootDir is the outer blueprint directory passed to Build, used for the local state default path. It is not the recursive baseDir used to resolve group-relative sources.
	rootDir string
}

func (rc *resolveContext) push(dir, name string) (func(), error) {
	key := dir + "::" + name
	for _, k := range rc.stack {
		if k == key {
			return nil, fmt.Errorf("circular group use: %s -> %s", strings.Join(rc.stack, " -> "), key)
		}
	}
	rc.stack = append(rc.stack, key)
	return func() { rc.stack = rc.stack[:len(rc.stack)-1] }, nil
}

// parseGroupDir returns dir merged as a Blueprint (every .hcl file directly inside it, see blueprint.ParseDir), parsing it at most once per Build() call regardless of how many `use` blocks reference dir.
func (rc *resolveContext) parseGroupDir(dir string) (*blueprint.Blueprint, error) {
	if bp, ok := rc.groupDirs[dir]; ok {
		return bp, nil
	}
	bp, err := blueprint.ParseDir(dir)
	if err != nil {
		return nil, err
	}
	if rc.groupDirs == nil {
		rc.groupDirs = map[string]*blueprint.Blueprint{}
	}
	rc.groupDirs[dir] = bp
	return bp, nil
}

// inspect returns dir's module schema (see module.Inspect), inspecting it at most once per Build() call regardless of how many nodes share dir as their source (the backend_config pattern docs/blueprint.md documents for reusing one module across instances).
func (rc *resolveContext) inspect(dir string) (*module.Schema, error) {
	if s, ok := rc.schemas[dir]; ok {
		return s, nil
	}
	s, err := module.Inspect(dir)
	if err != nil {
		return nil, err
	}
	if rc.schemas == nil {
		rc.schemas = map[string]*module.Schema{}
	}
	rc.schemas[dir] = s
	return s, nil
}

// loadGroupDef returns the group definition named groupName from dir, a group source directory (see resolveContext.parseGroupDir for how dir's .hcl files are merged and cached), along with every `runtime` block declared alongside it in that same directory: a group's own internal nodes/uses can only reference runtimes declared there (see blueprint.validateRuntimes), never ones declared in whichever outer scope happens to instantiate the group.
func loadGroupDef(rc *resolveContext, dir, groupName string) (*blueprint.Group, []blueprint.Runtime, error) {
	bp, err := rc.parseGroupDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading group source directory %s: %w", dir, err)
	}
	for i := range bp.Groups {
		if bp.Groups[i].Name == groupName {
			return &bp.Groups[i], bp.Runtimes, nil
		}
	}
	return nil, nil, fmt.Errorf("no group named %q found in %s", groupName, dir)
}

// synthesizeSchema turns a group's (already namespace-qualified) Export block into a module.Schema (the same shape module.Inspect produces for a real Terraform module) after validating every export mapping against the real schemas of the internal nodes it references. Synthesized Variables carry no Type: a fan-out input may map to internal targets with different declared types, so there is no single type to check at the export boundary. The real per-target type check still happens once an edge is rewritten to its actual leaf target (see engine.checkType), so this loses no safety, only reports a mismatch one step later.
func synthesizeSchema(export blueprint.Export, internal *Graph) (*module.Schema, error) {
	schema := &module.Schema{
		Variables: make(map[string]module.Variable, len(export.Inputs)),
		Outputs:   make(map[string]bool, len(export.Outputs)),
	}

	for _, in := range export.Inputs {
		if len(in.To) == 0 {
			return nil, fmt.Errorf("export input %q has no targets", in.Name)
		}
		required := false
		for _, ref := range in.To {
			target, ok := internal.Nodes[ref.Node]
			if !ok {
				return nil, fmt.Errorf("export input %q: internal node %q does not exist", in.Name, ref.Node)
			}
			v, ok := target.Schema.Variables[ref.Name]
			if !ok {
				return nil, fmt.Errorf("export input %q: node %q has no input variable %q", in.Name, ref.Node, ref.Name)
			}
			if v.Required {
				required = true
			}
		}
		schema.Variables[in.Name] = module.Variable{Name: in.Name, Required: required}
	}

	for _, out := range export.Outputs {
		target, ok := internal.Nodes[out.From.Node]
		if !ok {
			return nil, fmt.Errorf("export output %q: internal node %q does not exist", out.Name, out.From.Node)
		}
		if !target.Schema.HasOutput(out.From.Name) {
			return nil, fmt.Errorf("export output %q: node %q has no output %q", out.Name, out.From.Node, out.From.Name)
		}
		schema.Outputs[out.Name] = true
	}

	return schema, nil
}

// resolveExport resolves every reference in a group's own Export block down to real, namespace-qualified leaf ports. A reference may point at a plain internal node (just qualified) or at one of this same group's own use instances (uses), resolved through that instance's own already-resolved export, exactly as an edge endpoint would be (see resolveExportEndpoint). This is what makes an export forwarding a nested group's port (`use.inner.output.v`) work: by the time it's this group's own turn to be validated, uses[...] already holds fully-resolved, real leaf references, however deep the nesting.
func resolveExport(exp blueprint.Export, uses map[string]useInfo, qualify func(string) string) (blueprint.Export, error) {
	var out blueprint.Export
	for _, in := range exp.Inputs {
		var targets []blueprint.PortRef
		for _, ref := range in.To {
			resolved, err := resolveExportEndpoint(ref, uses, qualify)
			if err != nil {
				return blueprint.Export{}, fmt.Errorf("export input %q: %w", in.Name, err)
			}
			targets = append(targets, resolved...)
		}
		out.Inputs = append(out.Inputs, blueprint.ExportInput{Name: in.Name, To: targets})
	}
	for _, o := range exp.Outputs {
		resolved, err := resolveExportEndpoint(o.From, uses, qualify)
		if err != nil {
			return blueprint.Export{}, fmt.Errorf("export output %q: %w", o.Name, err)
		}
		if len(resolved) != 1 {
			return blueprint.Export{}, fmt.Errorf("export output %q must resolve to exactly one port, got %d", o.Name, len(resolved))
		}
		out.Outputs = append(out.Outputs, blueprint.ExportOutput{Name: o.Name, From: resolved[0]})
	}
	return out, nil
}

// resolveExportEndpoint resolves one export mapping endpoint (an ExportOutput.From, or one element of an ExportInput.To).
func resolveExportEndpoint(ref blueprint.PortRef, uses map[string]useInfo, qualify func(string) string) ([]blueprint.PortRef, error) {
	if ref.Entity != blueprint.EntityUse {
		r := ref
		r.Node = qualify(ref.Node)
		return []blueprint.PortRef{r}, nil
	}

	info, ok := uses[ref.Node]
	if !ok {
		return nil, fmt.Errorf("reference to unknown use instance %q", ref.Node)
	}

	if ref.Kind == blueprint.PortOutput {
		for _, o := range info.export.Outputs {
			if o.Name == ref.Name {
				return []blueprint.PortRef{o.From}, nil
			}
		}
		return nil, fmt.Errorf("use.%s.output.%s is not exported by this group", ref.Node, ref.Name)
	}

	for _, in := range info.export.Inputs {
		if in.Name == ref.Name {
			return append([]blueprint.PortRef(nil), in.To...), nil
		}
	}
	return nil, fmt.Errorf("use.%s.input.%s is not exported by this group", ref.Node, ref.Name)
}

// useInfo describes how edges in the scope that instantiated a group should resolve references to that instance: export gives the qualified port mapping for explicit data edges; roots/sinks (both already namespace-qualified) give the internal entry/exit points a bare, ordering-only edge into or out of the instance expands to. This is inferred directly from the group's internal graph shape, no export declaration needed, since "who must run first/last" is structural, not semantic, unlike "who needs this value" (which export.Inputs' fan-out exists for).
type useInfo struct {
	export blueprint.Export
	roots  []string
	sinks  []string
}

// rewriteEdge rewrites an edge declared in some scope into zero or more real, fully-qualified edges: a plain node endpoint is just namespace-qualified, while an endpoint referencing a use instance is resolved through that instance's useInfo (fanning out into multiple edges when either the export mapping or a bare group reference's root/sink set has more than one member).
func rewriteEdge(e blueprint.Edge, uses map[string]useInfo, qualify func(string) string) ([]blueprint.Edge, error) {
	froms, err := rewriteEndpoint(e.From, uses, qualify, false)
	if err != nil {
		return nil, fmt.Errorf("%s -> %s: %w", e.From, e.To, err)
	}
	tos, err := rewriteEndpoint(e.To, uses, qualify, true)
	if err != nil {
		return nil, fmt.Errorf("%s -> %s: %w", e.From, e.To, err)
	}
	if len(froms) > 1 && len(tos) > 1 {
		return nil, fmt.Errorf("%s -> %s: cannot fan out on both sides of an edge", e.From, e.To)
	}

	out := make([]blueprint.Edge, 0, len(froms)*len(tos))
	for _, f := range froms {
		for _, t := range tos {
			out = append(out, blueprint.Edge{From: f, To: t})
		}
	}
	return out, nil
}

// rewriteEndpoint resolves one edge endpoint. wantRoots selects, for a bare (ordering-only) reference to a use instance, whether it expands to that instance's internal roots (true, used for the "to" side: a downstream group's entry points must wait) or sinks (false, used for the "from" side: everything downstream must wait for the upstream group's exit points).
func rewriteEndpoint(ref blueprint.PortRef, uses map[string]useInfo, qualify func(string) string, wantRoots bool) ([]blueprint.PortRef, error) {
	if ref.Entity != blueprint.EntityUse {
		r := ref
		r.Node = qualify(ref.Node)
		return []blueprint.PortRef{r}, nil
	}

	info, ok := uses[ref.Node]
	if !ok {
		return nil, fmt.Errorf("reference to unknown use instance %q", ref.Node)
	}

	if !ref.IsPort() {
		names := info.sinks
		if wantRoots {
			names = info.roots
		}
		refs := make([]blueprint.PortRef, len(names))
		for i, n := range names {
			refs[i] = blueprint.PortRef{Node: n}
		}
		return refs, nil
	}

	if ref.Kind == blueprint.PortOutput {
		for _, o := range info.export.Outputs {
			if o.Name == ref.Name {
				return []blueprint.PortRef{o.From}, nil
			}
		}
		return nil, fmt.Errorf("use.%s.output.%s is not exported by this group", ref.Node, ref.Name)
	}

	for _, in := range info.export.Inputs {
		if in.Name == ref.Name {
			return append([]blueprint.PortRef(nil), in.To...), nil
		}
	}
	return nil, fmt.Errorf("use.%s.input.%s is not exported by this group", ref.Node, ref.Name)
}

// applyUseVars rewrites a use block's literal vars through the instance's already-resolved export onto the leaf nodes' Vars maps. Keys are export input names; each value is written onto every leaf the export names (fan-out included). Unknown export names are an Error rather than skipped, because Validate only sees leaf variable names and would otherwise accept a key that never landed. A leaf that already has that Vars key (group-body node.vars, a nested use.vars, or two export inputs targeting the same leaf) is also an Error: an input is a single slot. Does not mutate vars itself.
func applyUseVars(vars map[string]any, export blueprint.Export, nodes map[string]*Node, instanceAs string) error {
	if len(vars) == 0 {
		return nil
	}

	byName := make(map[string][]blueprint.PortRef, len(export.Inputs))
	for _, in := range export.Inputs {
		byName[in.Name] = in.To
	}

	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		targets, ok := byName[key]
		if !ok {
			return fmt.Errorf("use.%s.vars.%s is not an export input of this group", instanceAs, key)
		}
		val := vars[key]
		for _, ref := range targets {
			node, ok := nodes[ref.Node]
			if !ok {
				return fmt.Errorf("use.%s.vars.%s: export input maps to unknown node %q", instanceAs, key, ref.Node)
			}
			if node.Vars == nil {
				node.Vars = map[string]any{}
			}
			if _, exists := node.Vars[ref.Name]; exists {
				return fmt.Errorf("node.%s.input.%s: set by more than one vars source; remove extras", ref.Node, ref.Name)
			}
			node.Vars[ref.Name] = val
		}
	}
	return nil
}
