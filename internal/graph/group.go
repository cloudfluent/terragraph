package graph

import (
	"fmt"
	"github.com/hashicorp/hcl/v2"
	"sort"
	"strings"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

// resolveContext tracks state for one Build() call: in-progress group resolutions (by absolute source directory + group name), to catch group self-reference cycles, plus per-directory caches so a group source directory or a node's module directory and file mode is only ever read and parsed once no matter how many times it's referenced (multiple `use` instances of the same group, or multiple nodes sharing one `source` via backend_config). Both caches are safe uncontended: Build runs entirely single-threaded, and every goroutine terragraph ever spawns (see engine.runLevels) starts only after the graph it walks has already been fully built.
type resolveContext struct {
	evaluation *hcl.EvalContext
	// observation skips execution-only validation while retaining the existing recursive source resolver.
	observation bool
	stack       []string
	groupDirs   map[string]*blueprint.Blueprint
	schemas     map[schemaKey]*module.Schema
	// rootDir is the outer blueprint directory passed to Build, used for the local state default path. It is not the recursive baseDir used to resolve group-relative sources.
	rootDir string
	// fallbackBinary is the root default runtime or CLI fallback, shared by all expanded groups.
	fallbackBinary string
	// rootVendorDir keeps every expanded remote leaf in the calling blueprint's managed area.
	rootVendorDir string
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

// parseGroupDir returns dir merged as a Blueprint (eligible .hcl files directly inside it, see blueprint.ParseDir), parsing it at most once per Build() call regardless of how many `use` blocks reference dir.
func (rc *resolveContext) parseGroupDir(dir string) (*blueprint.Blueprint, error) {
	if bp, ok := rc.groupDirs[dir]; ok {
		return bp, nil
	}
	bp, err := blueprint.ParseDir(dir, rc.evaluation)
	if err != nil {
		return nil, err
	}
	if len(bp.Plugins) > 0 {
		return nil, fmt.Errorf("group source %s: plugin declarations belong in the root blueprint", dir)
	}
	if rc.groupDirs == nil {
		rc.groupDirs = map[string]*blueprint.Blueprint{}
	}
	rc.groupDirs[dir] = bp
	return bp, nil
}

// schemaKey keeps a shared source under two runtimes from reusing the other runtime's ports or sensitivity.
type schemaKey struct {
	dir  string
	mode module.FileMode
}

// inspect reuses a schema only within the same source directory and runtime file mode for one Build call.
func (rc *resolveContext) inspect(dir string, mode module.FileMode) (*module.Schema, error) {
	key := schemaKey{dir: dir, mode: mode}
	if s, ok := rc.schemas[key]; ok {
		return s, nil
	}
	s, err := module.Inspect(dir, mode)
	if err != nil {
		return nil, err
	}
	if rc.schemas == nil {
		rc.schemas = map[schemaKey]*module.Schema{}
	}
	rc.schemas[key] = s
	return s, nil
}

// loadGroupDef returns the group definition named groupName from dir, a group source directory (see resolveContext.parseGroupDir for how dir's .hcl files are merged and cached), along with every `runtime` block declared alongside it in that same directory: a group's own internal nodes/uses can only reference runtimes declared there (see blueprint.validateRuntimes), never ones declared in whichever outer scope happens to instantiate the group. It also returns the directory's own top-level contracts (producer/consumer blocks outside any group body), which ride with the group like the contracts inside its own body do, and its vendor configuration for compatibility source lookup.
func loadGroupDef(rc *resolveContext, dir, groupName string) (*blueprint.Group, []blueprint.Runtime, *blueprint.Contracts, *blueprint.VendorConfig, error) {
	bp, err := rc.parseGroupDir(dir)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("reading group source directory %s: %w", dir, err)
	}
	for i := range bp.Groups {
		if bp.Groups[i].Name == groupName {
			return &bp.Groups[i], bp.Runtimes, bp.Contracts, bp.Vendor, nil
		}
	}
	return nil, nil, nil, nil, fmt.Errorf("no group named %q found in %s", groupName, dir)
}

// hop is one export traversal recorded while references resolve, before its final consumer is known: a use.vars supply reports the export block's own declaration (blockLoc, the thing to edit to change the mapping), while a rewritten edge reports the endpoint mapping attribute (attrLoc, the `to`/`from` the edge actually resolved through). Both pins ride together until materialization picks one.
type hop struct {
	via      string
	blockLoc blueprint.Loc
	attrLoc  blueprint.Loc
}

// resolvedRef is one final leaf port plus the hops that reached it, outermost declaration first: appending deeper hops in front preserves original-declaration-to-leaf order no matter how many levels nested uses add.
type resolvedRef struct {
	ref  blueprint.PortRef
	hops []hop
}

// resolvedExport is a group's Export after resolution: every port reference is a real leaf port, and each carries the hops traversed through nested uses. It replaces blueprint.Export in useInfo because the resolved form must also keep the original group-file Locs (blueprint.Export's typed copy drops them) for mapping provenance.
type resolvedExport struct {
	inputs  []resolvedInput
	outputs []resolvedOutput
}

// resolvedInput is one export input after fan-out resolution; loc/toLoc pin the block and its `to` attribute in the declaring group file.
type resolvedInput struct {
	name    string
	loc     blueprint.Loc
	toLoc   blueprint.Loc
	targets []resolvedRef
}

// resolvedOutput is one export output after passthrough resolution; loc/fromLoc pin the block and its `from` attribute.
type resolvedOutput struct {
	name    string
	loc     blueprint.Loc
	fromLoc blueprint.Loc
	from    resolvedRef
}

// prependHop returns t's port with h in front of t's hops, copying rather than mutating: a resolved export is shared by every later consumer (a nested resolveExport, rewriteEdge, applyUseVars), so assembling provenance must never write into a shared backing array.
func prependHop(h hop, t resolvedRef) resolvedRef {
	return resolvedRef{ref: t.ref, hops: append([]hop{h}, t.hops...)}
}

// hopPin selects which declaration site a hop's MappingStep reports once the consumer is known.
type hopPin int

const (
	// blockPin reports the export block: a use.vars supply is edited by changing what the mapping is, not where an endpoint pointed.
	blockPin hopPin = iota
	// attrPin reports the from/to attribute: an edge's own block already says where the edge was written; the mapping's fix-site is the attribute it resolved through.
	attrPin
)

// mappingSteps materializes hops as public MappingSteps under the chosen pin; empty stays nil so plain connections read as unmapped.
func mappingSteps(hops []hop, pin hopPin) []MappingStep {
	if len(hops) == 0 {
		return nil
	}
	steps := make([]MappingStep, len(hops))
	for i, h := range hops {
		steps[i] = MappingStep{Via: h.via, Decl: h.blockLoc}
		if pin == attrPin {
			steps[i].Decl = h.attrLoc
		}
	}
	return steps
}

// synthesizeSchema turns a group's (already namespace-qualified and resolved) Export into a module.Schema (the same shape module.Inspect produces for a real Terraform module) after validating every export mapping against the real schemas of the internal nodes it references. Synthesized Variables carry no Type: a fan-out input may map to internal targets with different declared types, so there is no single type to check at the export boundary. The real per-target type check still happens once an edge is rewritten to its actual leaf target (see engine.checkType), so this loses no safety, only reports a mismatch one step later.
func synthesizeSchema(export resolvedExport, internal *Graph) (*module.Schema, error) {
	schema := &module.Schema{
		Variables: make(map[string]module.Variable, len(export.inputs)),
		Outputs:   make(map[string]bool, len(export.outputs)),
	}

	for _, in := range export.inputs {
		if len(in.targets) == 0 {
			return nil, fmt.Errorf("export input %q has no targets", in.name)
		}
		required := false
		for _, t := range in.targets {
			target, ok := internal.Nodes[t.ref.Node]
			if !ok {
				return nil, fmt.Errorf("export input %q: internal node %q does not exist", in.name, t.ref.Node)
			}
			v, ok := target.Schema.Variables[t.ref.Name]
			if !ok {
				return nil, fmt.Errorf("export input %q: node %q has no input variable %q", in.name, t.ref.Node, t.ref.Name)
			}
			if v.Required {
				required = true
			}
		}
		schema.Variables[in.name] = module.Variable{Name: in.name, Required: required}
	}

	for _, out := range export.outputs {
		target, ok := internal.Nodes[out.from.ref.Node]
		if !ok {
			return nil, fmt.Errorf("export output %q: internal node %q does not exist", out.name, out.from.ref.Node)
		}
		if !target.Schema.HasOutput(out.from.ref.Name) {
			return nil, fmt.Errorf("export output %q: node %q has no output %q", out.name, out.from.ref.Node, out.from.ref.Name)
		}
		schema.Outputs[out.name] = true
	}

	return schema, nil
}

// resolveExport resolves every reference in a group's own Export block down to real, namespace-qualified leaf ports. A reference may point at a plain internal node (just qualified) or at one of this same group's own use instances (uses), resolved through that instance's own already-resolved export, exactly as an edge endpoint would be (see resolveExportEndpoint). This is what makes an export forwarding a nested group's port (`use.inner.output.v`) work: by the time it's this group's own turn to be validated, uses[...] already holds fully-resolved, real leaf references, however deep the nesting. Each traversal of a nested instance's export prepends its hop, so a mapping's full path from original declaration to leaf survives flattening.
func resolveExport(exp blueprint.Export, uses map[string]useInfo, qualify func(string) string) (resolvedExport, error) {
	var out resolvedExport
	for _, in := range exp.Inputs {
		var targets []resolvedRef
		for _, ref := range in.To {
			resolved, err := resolveExportEndpoint(ref, uses, qualify)
			if err != nil {
				return resolvedExport{}, fmt.Errorf("export input %q: %w", in.Name, err)
			}
			targets = append(targets, resolved...)
		}
		out.inputs = append(out.inputs, resolvedInput{name: in.Name, loc: in.Loc, toLoc: in.ToLoc, targets: targets})
	}
	for _, o := range exp.Outputs {
		resolved, err := resolveExportEndpoint(o.From, uses, qualify)
		if err != nil {
			return resolvedExport{}, fmt.Errorf("export output %q: %w", o.Name, err)
		}
		if len(resolved) != 1 {
			return resolvedExport{}, fmt.Errorf("export output %q must resolve to exactly one port, got %d", o.Name, len(resolved))
		}
		out.outputs = append(out.outputs, resolvedOutput{name: o.Name, loc: o.Loc, fromLoc: o.FromLoc, from: resolved[0]})
	}
	return out, nil
}

// resolveExportEndpoint resolves one export mapping endpoint (an ExportOutput.From, or one element of an ExportInput.To).
func resolveExportEndpoint(ref blueprint.PortRef, uses map[string]useInfo, qualify func(string) string) ([]resolvedRef, error) {
	if ref.Entity != blueprint.EntityUse {
		r := ref
		r.Node = qualify(ref.Node)
		return []resolvedRef{{ref: r}}, nil
	}

	info, ok := uses[ref.Node]
	if !ok {
		return nil, fmt.Errorf("reference to unknown use instance %q", ref.Node)
	}

	return usePortRefs(ref, info)
}

// usePortRefs resolves a port reference through a use instance's resolved export, prepending the hop this traversal adds: Via names the traversed export port as written in this scope, and both declaration pins come from the export entry of the instance named in the reference — its own group file, wherever that file lives. Export inputs fan out (one hop, many targets); outputs are 1:1.
func usePortRefs(ref blueprint.PortRef, info useInfo) ([]resolvedRef, error) {
	if ref.Kind == blueprint.PortOutput {
		for _, o := range info.export.outputs {
			if o.name == ref.Name {
				h := hop{via: "use." + ref.Node + ".output." + ref.Name, blockLoc: o.loc, attrLoc: o.fromLoc}
				return []resolvedRef{prependHop(h, o.from)}, nil
			}
		}
		return nil, fmt.Errorf("use.%s.output.%s is not exported by this group", ref.Node, ref.Name)
	}

	for _, in := range info.export.inputs {
		if in.name == ref.Name {
			h := hop{via: "use." + ref.Node + ".input." + ref.Name, blockLoc: in.loc, attrLoc: in.toLoc}
			refs := make([]resolvedRef, len(in.targets))
			for i, t := range in.targets {
				refs[i] = prependHop(h, t)
			}
			return refs, nil
		}
	}
	return nil, fmt.Errorf("use.%s.input.%s is not exported by this group", ref.Node, ref.Name)
}

// useInfo describes how edges in the scope that instantiated a group should resolve references to that instance: export gives the qualified port mapping for explicit data edges; roots/sinks (both already namespace-qualified) give the internal entry/exit points a bare, ordering-only edge into or out of the instance expands to. This is inferred directly from the group's internal graph shape, no export declaration needed, since "who must run first/last" is structural, not semantic, unlike "who needs this value" (which export.Inputs' fan-out exists for).
type useInfo struct {
	export resolvedExport
	roots  []string
	sinks  []string
}

// rewriteEdge rewrites an edge declared in some scope into zero or more real, fully-qualified edges: a plain node endpoint is just namespace-qualified, while an endpoint referencing a use instance is resolved through that instance's useInfo (fanning out into multiple edges when either the export mapping or a bare group reference's root/sink set has more than one member). Every rewritten copy keeps the original edge block's Loc — inspection must point at the declaration to edit — and the export hops its rewrite traversed, pinned at the mapping attributes; a fan-out gives each destination its own copy of both.
func rewriteEdge(e blueprint.Edge, uses map[string]useInfo, qualify func(string) string) ([]Edge, error) {
	froms, err := rewriteEndpoint(e.From, uses, qualify, false)
	if err != nil {
		return nil, fmt.Errorf("%s -> %s: %w", e.From, e.To, err)
	}
	tos, err := rewriteEndpoint(e.To, uses, qualify, true)
	if err != nil {
		return nil, fmt.Errorf("%s -> %s: %w", e.From, e.To, err)
	}
	if e.IsDataEdge() && len(froms) > 1 && len(tos) > 1 {
		return nil, fmt.Errorf("%s -> %s: cannot fan out on both sides of an edge", e.From, e.To)
	}

	out := make([]Edge, 0, len(froms)*len(tos))
	for _, f := range froms {
		for _, t := range tos {
			out = append(out, Edge{
				Edge:        blueprint.Edge{From: f.ref, To: t.ref, Loc: e.Loc},
				MappingPath: append(mappingSteps(f.hops, attrPin), mappingSteps(t.hops, attrPin)...),
			})
		}
	}
	return out, nil
}

// rewriteEndpoint resolves one edge endpoint. wantRoots selects, for a bare (ordering-only) reference to a use instance, whether it expands to that instance's internal roots (true, used for the "to" side: a downstream group's entry points must wait) or sinks (false, used for the "from" side: everything downstream must wait for the upstream group's exit points). A bare expansion traverses no export, so it adds no hop: ordering is structural, not a value mapping.
func rewriteEndpoint(ref blueprint.PortRef, uses map[string]useInfo, qualify func(string) string, wantRoots bool) ([]resolvedRef, error) {
	if ref.Entity != blueprint.EntityUse {
		r := ref
		r.Node = qualify(ref.Node)
		return []resolvedRef{{ref: r}}, nil
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
		refs := make([]resolvedRef, len(names))
		for i, n := range names {
			refs[i] = resolvedRef{ref: blueprint.PortRef{Node: n}}
		}
		return refs, nil
	}

	return usePortRefs(ref, info)
}

// applyUseVars rewrites a use block's literal vars through the instance's already-resolved export onto the leaf nodes' Vars maps. Keys are export input names; each value is written onto every leaf the export names (fan-out included). Unknown export names are an Error rather than skipped, because Validate only sees leaf variable names and would otherwise accept a key that never landed. A leaf that already has that Vars key (group-body node.vars, a nested use.vars, or two export inputs targeting the same leaf) is also an Error: an input is a single slot. Does not mutate vars itself. Alongside each value it records the supply's provenance: the declaring use's own vars Loc, and the mapping path from the export input block (block pin: that is the mapping to edit) down to the leaf, so a nested use.vars that propagated through an inner export keeps its own original declaration.
func applyUseVars(vars map[string]any, varsLocs map[string]blueprint.Loc, export resolvedExport, nodes map[string]*Node, instanceAs string) error {
	if len(vars) == 0 {
		return nil
	}

	byName := make(map[string]*resolvedInput, len(export.inputs))
	for i := range export.inputs {
		byName[export.inputs[i].name] = &export.inputs[i]
	}

	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		in, ok := byName[key]
		if !ok {
			return fmt.Errorf("use.%s.vars.%s is not an export input of this group", instanceAs, key)
		}
		val := vars[key]
		for _, target := range in.targets {
			node, ok := nodes[target.ref.Node]
			if !ok {
				return fmt.Errorf("use.%s.vars.%s: export input maps to unknown node %q", instanceAs, key, target.ref.Node)
			}
			if node.Vars == nil {
				node.Vars = map[string]any{}
			}
			if _, exists := node.Vars[target.ref.Name]; exists {
				return fmt.Errorf("node.%s.input.%s: set by more than one vars source; remove extras", target.ref.Node, target.ref.Name)
			}
			node.Vars[target.ref.Name] = val
			if node.VarSources == nil {
				node.VarSources = map[string]VarSource{}
			}
			node.VarSources[target.ref.Name] = VarSource{
				From:    "use_vars",
				Subject: "use." + instanceAs + ".vars." + key,
				Decl:    varsLocs[key],
				// The instance's own export input hop first (the mapping this key went through), then whatever deeper exports the target already traversed.
				MappingPath: append([]MappingStep{{Via: "use." + instanceAs + ".input." + key, Decl: in.loc}}, mappingSteps(target.hops, blockPin)...),
			}
		}
	}
	return nil
}
