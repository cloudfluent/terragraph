package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

// resolveContext tracks in-progress group resolutions (by absolute source directory + group name) to catch group self-reference cycles: a group that, directly or transitively, uses itself.
type resolveContext struct {
	stack []string
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

// loadGroupDef parses the .hcl files directly inside dir (not recursively; group source directories aren't tree-scanned) and returns the group definition named groupName.
func loadGroupDef(dir, groupName string) (*blueprint.Group, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading group source directory %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".hcl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		bp, err := blueprint.ParseFile(path)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		for i := range bp.Groups {
			if bp.Groups[i].Name == groupName {
				return &bp.Groups[i], nil
			}
		}
	}
	return nil, fmt.Errorf("no group named %q found in %s", groupName, dir)
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
