package blueprint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

var topSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "node", LabelNames: []string{"name"}},
		{Type: "edge"},
		{Type: "group", LabelNames: []string{"name"}},
		{Type: "use", LabelNames: []string{"name"}},
		{Type: "vendor"},
		{Type: "tfvars"},
	},
}

var vendorSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "directory", Required: false},
		{Name: "manifest_file", Required: false},
	},
}

var tfvarsSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "location", Required: false},
	},
}

var nodeSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "source", Required: true},
		{Name: "backend_config", Required: false},
		{Name: "vars", Required: false},
	},
}

var edgeSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "from", Required: true},
		{Name: "to", Required: true},
	},
}

var groupBodySchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "node", LabelNames: []string{"name"}},
		{Type: "edge"},
		{Type: "use", LabelNames: []string{"name"}},
		{Type: "export"},
	},
}

var useSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "as", Required: true},
		{Name: "source", Required: true},
	},
}

var exportBodySchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "input", LabelNames: []string{"name"}},
		{Type: "output", LabelNames: []string{"name"}},
	},
}

var exportInputSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "to", Required: true}},
}

var exportOutputSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "from", Required: true}},
}

// ParseFile reads and parses a blueprint HCL file at path into a Blueprint. It does not touch any Terraform module, it only extracts graph topology.
func ParseFile(path string) (*Blueprint, error) {
	bp := &Blueprint{}
	seenNodes := map[string]bool{}
	seenGroups := map[string]bool{}
	seenUses := map[string]bool{}

	if err := parseOneFile(path, bp, seenNodes, seenGroups, seenUses); err != nil {
		return nil, err
	}
	if err := validateEdges(bp, seenNodes, seenUses); err != nil {
		return nil, err
	}
	return bp, nil
}

// ParseDir reads and parses every .hcl file directly inside dir (not recursively) and merges them into a single Blueprint, the same way loadGroupDef already treats a group source directory: node/group/use names and the vendor block must be unique across the whole directory, not just within one file, and an edge in one file may reference a node or use instance declared in another. Files are visited in the order os.ReadDir returns them (lexical by name), so a duplicate-name error always names the second file, deterministically.
func ParseDir(dir string) (*Blueprint, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading blueprint directory: %w", err)
	}

	bp := &Blueprint{}
	seenNodes := map[string]bool{}
	seenGroups := map[string]bool{}
	seenUses := map[string]bool{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".hcl") {
			continue
		}
		if err := parseOneFile(filepath.Join(dir, e.Name()), bp, seenNodes, seenGroups, seenUses); err != nil {
			return nil, err
		}
	}

	if err := validateEdges(bp, seenNodes, seenUses); err != nil {
		return nil, err
	}
	return bp, nil
}

// LoadPath resolves path to a Blueprint and the base directory its node/group sources resolve against. If path names a directory, every .hcl file directly inside it is parsed and merged (see ParseDir) and baseDir is path itself. If path names a file, only that file is parsed (see ParseFile) and baseDir is its parent directory.
func LoadPath(path string) (*Blueprint, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("resolving blueprint path: %w", err)
	}
	if info.IsDir() {
		bp, err := ParseDir(path)
		return bp, path, err
	}
	bp, err := ParseFile(path)
	return bp, filepath.Dir(path), err
}

// parseOneFile parses one HCL file and merges its node/edge/group/use/vendor blocks into bp, checking name and vendor-block uniqueness against the caller-supplied seen-sets (shared across every file being merged into the same bp). Edge endpoint validation deliberately does not happen here: it happens once, in validateEdges, after every file that will ever contribute to bp has been merged, so an edge in one file may reference a node or use instance declared in another.
func parseOneFile(path string, bp *Blueprint, seenNodes, seenGroups, seenUses map[string]bool) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading blueprint file: %w", err)
	}

	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL(src, path)
	if diags.HasErrors() {
		return fmt.Errorf("parsing blueprint file: %s", diags.Error())
	}

	content, diags := file.Body.Content(topSchema)
	if diags.HasErrors() {
		return fmt.Errorf("reading blueprint file: %s", diags.Error())
	}

	for _, block := range content.Blocks {
		switch block.Type {
		case "node":
			node, err := parseNodeBlock(block)
			if err != nil {
				return err
			}
			if seenNodes[node.Name] {
				return fmt.Errorf("%s: duplicate node %q", block.DefRange, node.Name)
			}
			seenNodes[node.Name] = true
			bp.Nodes = append(bp.Nodes, node)
		case "edge":
			edge, err := parseEdgeBlock(block)
			if err != nil {
				return err
			}
			bp.Edges = append(bp.Edges, edge)
		case "group":
			group, err := parseGroupBlock(block)
			if err != nil {
				return err
			}
			if seenGroups[group.Name] {
				return fmt.Errorf("%s: duplicate group %q", block.DefRange, group.Name)
			}
			seenGroups[group.Name] = true
			bp.Groups = append(bp.Groups, group)
		case "use":
			use, err := parseUseBlock(block)
			if err != nil {
				return err
			}
			if seenUses[use.As] {
				return fmt.Errorf("%s: duplicate use instance name %q", block.DefRange, use.As)
			}
			seenUses[use.As] = true
			bp.Uses = append(bp.Uses, use)
		case "vendor":
			if bp.Vendor != nil {
				return fmt.Errorf("%s: duplicate vendor block", block.DefRange)
			}
			vc, err := parseVendorBlock(block)
			if err != nil {
				return err
			}
			bp.Vendor = vc
		case "tfvars":
			if bp.TFVars != nil {
				return fmt.Errorf("%s: duplicate tfvars block", block.DefRange)
			}
			tc, err := parseTFVarsBlock(block)
			if err != nil {
				return err
			}
			bp.TFVars = tc
		}
	}

	return nil
}

// validateEdges checks every edge's endpoints against the full set of nodes/use instances merged into bp so far. Run once, after all contributing files have been merged, so cross-file references resolve correctly.
func validateEdges(bp *Blueprint, seenNodes, seenUses map[string]bool) error {
	for _, edge := range bp.Edges {
		if err := validateEndpointKnown(edge.From, seenNodes, seenUses); err != nil {
			return err
		}
		if err := validateEndpointKnown(edge.To, seenNodes, seenUses); err != nil {
			return err
		}
	}
	return nil
}

// validateEndpointKnown checks that a PortRef's referenced entity was actually declared in the same scope: a node reference against declared nodes, a use reference against declared use instances.
func validateEndpointKnown(ref PortRef, seenNodes, seenUses map[string]bool) error {
	if ref.Entity == EntityUse {
		if !seenUses[ref.Node] {
			return fmt.Errorf("reference to unknown use instance %q in %s", ref.Node, ref)
		}
		return nil
	}
	if !seenNodes[ref.Node] {
		return fmt.Errorf("reference to unknown node %q in %s", ref.Node, ref)
	}
	return nil
}

// parseVendorBlock parses the optional `vendor { }` block: project-wide layout for vendored third-party sources. Both fields are optional; a missing block, or missing fields within it, mean the DefaultVendor* constants apply (see Blueprint.VendorDirectory/VendorManifestFile).
func parseVendorBlock(block *hcl.Block) (*VendorConfig, error) {
	content, diags := block.Body.Content(vendorSchema)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s: %s", block.DefRange, diags.Error())
	}

	vc := &VendorConfig{}

	if attr, ok := content.Attributes["directory"]; ok {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() || val.Type() != cty.String {
			return nil, fmt.Errorf("%s: directory must be a literal string", attr.Range)
		}
		vc.Directory = val.AsString()
	}
	if attr, ok := content.Attributes["manifest_file"]; ok {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() || val.Type() != cty.String {
			return nil, fmt.Errorf("%s: manifest_file must be a literal string", attr.Range)
		}
		vc.ManifestFile = val.AsString()
	}

	return vc, nil
}

// parseTFVarsBlock parses the optional `tfvars { }` block: project-wide selection of where the engine writes each node's resolved input values (see Blueprint.TFVarsLocation). A missing block, or a missing location field within it, means TFVarsLocationWorkdir applies.
func parseTFVarsBlock(block *hcl.Block) (*TFVarsConfig, error) {
	content, diags := block.Body.Content(tfvarsSchema)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s: %s", block.DefRange, diags.Error())
	}

	tc := &TFVarsConfig{}

	if attr, ok := content.Attributes["location"]; ok {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() || val.Type() != cty.String {
			return nil, fmt.Errorf("%s: location must be a literal string", attr.Range)
		}
		loc := TFVarsLocation(val.AsString())
		if loc != TFVarsLocationWorkdir && loc != TFVarsLocationModule {
			return nil, fmt.Errorf("%s: location must be %q or %q, got %q", attr.Range, TFVarsLocationWorkdir, TFVarsLocationModule, loc)
		}
		tc.Location = loc
	}

	return tc, nil
}

// nameRegexp constrains node, group, and use-instance names to characters that are always safe as a single path segment on every platform terragraph targets. Both tfvars locations turn a node's name into part of a filename (see Blueprint.TFVarsLocation), and a use instance's "as" name becomes a namespace prefix joined with "." into every node it expands (see graph.Build); an unconstrained name could otherwise inject a path separator or a ".." segment.
var nameRegexp = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateName checks a node/group/use name against nameRegexp, labeling the error with what kind of name it is (for a clearer message) and the HCL range that declared it.
func validateName(kind, name string, rng hcl.Range) error {
	if !nameRegexp.MatchString(name) {
		return fmt.Errorf("%s: %s name %q is invalid: must contain only letters, digits, underscores, and hyphens", rng, kind, name)
	}
	return nil
}

func parseNodeBlock(block *hcl.Block) (Node, error) {
	content, diags := block.Body.Content(nodeSchema)
	if diags.HasErrors() {
		return Node{}, fmt.Errorf("%s: %s", block.DefRange, diags.Error())
	}
	if err := validateName("node", block.Labels[0], block.DefRange); err != nil {
		return Node{}, err
	}

	sourceAttr := content.Attributes["source"]
	val, diags := sourceAttr.Expr.Value(nil)
	if diags.HasErrors() {
		return Node{}, fmt.Errorf("%s: source must be a literal string: %s", sourceAttr.Range, diags.Error())
	}
	if val.Type() != cty.String {
		return Node{}, fmt.Errorf("%s: source must be a string", sourceAttr.Range)
	}

	var backendConfig map[string]string
	if attr, ok := content.Attributes["backend_config"]; ok {
		var err error
		backendConfig, err = parseBackendConfig(attr)
		if err != nil {
			return Node{}, err
		}
	}

	var vars map[string]any
	if attr, ok := content.Attributes["vars"]; ok {
		var err error
		vars, err = parseVarsAttr(attr)
		if err != nil {
			return Node{}, err
		}
	}

	return Node{
		Name:          block.Labels[0],
		Source:        val.AsString(),
		BackendConfig: backendConfig,
		Vars:          vars,
	}, nil
}

// parseVarsAttr evaluates the optional vars attribute: literal input values declared directly on a node, keyed by the target variable's name (see Node.Vars). The expression is evaluated with no variables or functions in scope, so a reference to another node's or use instance's output (e.g. node.vpc.output.vpc_id) fails to parse here exactly as intended: that kind of value must come from a real edge, not vars, since only an edge records the dependency the engine needs to sequence execution and wait for the value to actually exist.
func parseVarsAttr(attr *hcl.Attribute) (map[string]any, error) {
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return nil, fmt.Errorf(
			"%s: vars must be a literal object of variable name to value, with no references to node/use outputs (use an edge for those): %s",
			attr.Range, diags.Error(),
		)
	}
	if !val.Type().IsObjectType() && !val.Type().IsMapType() {
		return nil, fmt.Errorf("%s: vars must be an object/map of variable name to value", attr.Range)
	}

	result := make(map[string]any)
	it := val.ElementIterator()
	for it.Next() {
		k, v := it.Element()
		// Round-trips through JSON rather than a direct cty->Go conversion: this is the exact inverse of how Engine.checkVarType later decodes a declared variable's cty.Type from a plain Go value, so a vars entry and a data edge's resolved value end up represented identically (map[string]any, []any, string, float64, bool, nil) by the time either reaches engine.resolveInputs.
		data, err := ctyjson.Marshal(v, v.Type())
		if err != nil {
			return nil, fmt.Errorf("%s: vars.%s: %s", attr.Range, k.AsString(), err)
		}
		var goVal any
		if err := json.Unmarshal(data, &goVal); err != nil {
			return nil, fmt.Errorf("%s: vars.%s: %s", attr.Range, k.AsString(), err)
		}
		result[k.AsString()] = goVal
	}
	return result, nil
}

// parseBackendConfig evaluates the optional backend_config attribute, an object/map of string keys to string values passed through as `terraform init -backend-config=key=value` flags.
func parseBackendConfig(attr *hcl.Attribute) (map[string]string, error) {
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s: backend_config must be a literal map of strings: %s", attr.Range, diags.Error())
	}
	if !val.CanIterateElements() {
		return nil, fmt.Errorf("%s: backend_config must be a map/object of strings", attr.Range)
	}

	result := make(map[string]string)
	it := val.ElementIterator()
	for it.Next() {
		k, v := it.Element()
		str, err := convert.Convert(v, cty.String)
		if err != nil {
			return nil, fmt.Errorf("%s: backend_config.%s must be a string: %s", attr.Range, k.AsString(), err)
		}
		if str.IsNull() {
			return nil, fmt.Errorf("%s: backend_config.%s must not be null", attr.Range, k.AsString())
		}
		result[k.AsString()] = str.AsString()
	}
	return result, nil
}

func parseEdgeBlock(block *hcl.Block) (Edge, error) {
	content, diags := block.Body.Content(edgeSchema)
	if diags.HasErrors() {
		return Edge{}, fmt.Errorf("%s: %s", block.DefRange, diags.Error())
	}

	from, err := parseNodeOrPortRef(content.Attributes["from"])
	if err != nil {
		return Edge{}, err
	}
	to, err := parseNodeOrPortRef(content.Attributes["to"])
	if err != nil {
		return Edge{}, err
	}

	// Either both endpoints name a specific port (explicit data edge) or neither does (implicit, ordering-only edge). Mixing the two would be ambiguous: a value with nowhere declared to land, or an input with no declared source.
	if from.IsPort() != to.IsPort() {
		return Edge{}, fmt.Errorf(
			"%s: \"from\" and \"to\" must both reference specific ports (node.<name>.output.<attr> / node.<name>.input.<attr>) for a data edge, or both reference bare nodes (node.<name>) for an ordering-only edge; got %s -> %s",
			block.DefRange, from, to,
		)
	}

	if from.IsPort() {
		if from.Kind != PortOutput {
			return Edge{}, fmt.Errorf("%s: \"from\" must reference an output port (node.<name>.output.<attr>), got %s", content.Attributes["from"].Range, from)
		}
		if to.Kind != PortInput {
			return Edge{}, fmt.Errorf("%s: \"to\" must reference an input port (node.<name>.input.<attr>), got %s", content.Attributes["to"].Range, to)
		}
	}

	return Edge{From: from, To: to}, nil
}

// parseNodeOrPortRef extracts either a bare reference (node.<name> or use.<name>, used for an ordering-only edge) or a specific port reference (node.<name>.<output|input>.<attr> or use.<name>.<output|input>.<attr>) from an attribute's expression. The expression is never evaluated as a value: it is read purely as a traversal, mirroring how Terraform itself resolves resource references like aws_instance.foo.id. A group instance (use.<name>) resolves to exactly the same PortRef shape as a node (see EntityKind) because from every caller's perspective a group instance is indistinguishable from a node once its Export is validated.
func parseNodeOrPortRef(attr *hcl.Attribute) (PortRef, error) {
	traversal, diags := hcl.AbsTraversalForExpr(attr.Expr)
	if diags.HasErrors() {
		return PortRef{}, fmt.Errorf("%s: must be a reference of the form node.<name> or use.<name>, optionally with .output.<attr> / .input.<attr>, not an expression", attr.Range)
	}

	root, ok := traversal[0].(hcl.TraverseRoot)
	if !ok {
		return PortRef{}, fmt.Errorf("%s: reference must start with \"node\" or \"use\"", attr.Range)
	}

	var entity EntityKind
	switch root.Name {
	case "node":
		entity = EntityNode
	case "use":
		entity = EntityUse
	default:
		return PortRef{}, fmt.Errorf("%s: reference must start with \"node\" or \"use\", got %q", attr.Range, root.Name)
	}

	name, err := traverseAttrName(traversal[1], attr.Range)
	if err != nil {
		return PortRef{}, err
	}

	switch len(traversal) {
	case 2:
		// node.<name> / use.<name>: bare reference, ordering-only.
		return PortRef{Entity: entity, Node: name}, nil
	case 4:
		// node.<name>.<output|input>.<attr> / use.<name>.<output|input>.<attr>
		kindName, err := traverseAttrName(traversal[2], attr.Range)
		if err != nil {
			return PortRef{}, err
		}
		portName, err := traverseAttrName(traversal[3], attr.Range)
		if err != nil {
			return PortRef{}, err
		}

		var kind PortKind
		switch kindName {
		case string(PortOutput):
			kind = PortOutput
		case string(PortInput):
			kind = PortInput
		default:
			return PortRef{}, fmt.Errorf("%s: expected \"output\" or \"input\", got %q", attr.Range, kindName)
		}

		return PortRef{Entity: entity, Node: name, Kind: kind, Name: portName}, nil
	default:
		return PortRef{}, fmt.Errorf("%s: expected <keyword>.<name> or <keyword>.<name>.<output|input>.<attr>, got %d path segments", attr.Range, len(traversal))
	}
}

// parsePortRefList extracts either a single port reference or a static list of them from an attribute's expression. Used by export input mappings: a single exposed value sometimes needs to fan out to several internal targets, which, unlike execution ordering, can't be inferred from graph structure alone and so must be spelled out explicitly.
func parsePortRefList(attr *hcl.Attribute) ([]PortRef, error) {
	if ref, err := parseNodeOrPortRef(attr); err == nil {
		if !ref.IsPort() {
			return nil, fmt.Errorf("%s: must reference a specific port, got %s", attr.Range, ref)
		}
		return []PortRef{ref}, nil
	}

	exprs, diags := hcl.ExprList(attr.Expr)
	if diags.HasErrors() || len(exprs) == 0 {
		return nil, fmt.Errorf("%s: must be a port reference (e.g. node.<name>.input.<attr>) or a list of them", attr.Range)
	}

	refs := make([]PortRef, 0, len(exprs))
	for _, e := range exprs {
		ref, err := parseNodeOrPortRef(&hcl.Attribute{Expr: e, Range: attr.Range})
		if err != nil {
			return nil, err
		}
		if !ref.IsPort() {
			return nil, fmt.Errorf("%s: must reference a specific port, got %s", attr.Range, ref)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func traverseAttrName(step hcl.Traverser, rng hcl.Range) (string, error) {
	attr, ok := step.(hcl.TraverseAttr)
	if !ok {
		return "", fmt.Errorf("%s: expected a simple attribute reference", rng)
	}
	return attr.Name, nil
}

// parseGroupBlock parses a group definition: its internal nodes, edges, nested use instantiations, and its export interface. Internal references are validated for existence here (does the referenced node/use instance exist in this group), but not against any real Terraform module schema. That happens later, once the group is actually instantiated, using the same module.Inspect + graph.Validate machinery a top-level blueprint uses.
func parseGroupBlock(block *hcl.Block) (Group, error) {
	content, diags := block.Body.Content(groupBodySchema)
	if diags.HasErrors() {
		return Group{}, fmt.Errorf("%s: %s", block.DefRange, diags.Error())
	}

	if err := validateName("group", block.Labels[0], block.DefRange); err != nil {
		return Group{}, err
	}

	g := Group{Name: block.Labels[0]}
	seenNodes := map[string]bool{}
	seenUses := map[string]bool{}
	sawExport := false

	for _, b := range content.Blocks {
		switch b.Type {
		case "node":
			n, err := parseNodeBlock(b)
			if err != nil {
				return Group{}, err
			}
			if seenNodes[n.Name] {
				return Group{}, fmt.Errorf("%s: duplicate node %q in group %q", b.DefRange, n.Name, g.Name)
			}
			seenNodes[n.Name] = true
			g.Nodes = append(g.Nodes, n)
		case "edge":
			e, err := parseEdgeBlock(b)
			if err != nil {
				return Group{}, err
			}
			g.Edges = append(g.Edges, e)
		case "use":
			u, err := parseUseBlock(b)
			if err != nil {
				return Group{}, err
			}
			if seenUses[u.As] {
				return Group{}, fmt.Errorf("%s: duplicate use instance name %q in group %q", b.DefRange, u.As, g.Name)
			}
			seenUses[u.As] = true
			g.Uses = append(g.Uses, u)
		case "export":
			if sawExport {
				return Group{}, fmt.Errorf("%s: group %q has more than one export block", b.DefRange, g.Name)
			}
			sawExport = true
			exp, err := parseExportBlock(b)
			if err != nil {
				return Group{}, err
			}
			g.Export = exp
		}
	}

	for _, edge := range g.Edges {
		if err := validateEndpointKnown(edge.From, seenNodes, seenUses); err != nil {
			return Group{}, fmt.Errorf("group %q: %w", g.Name, err)
		}
		if err := validateEndpointKnown(edge.To, seenNodes, seenUses); err != nil {
			return Group{}, fmt.Errorf("group %q: %w", g.Name, err)
		}
	}
	for _, in := range g.Export.Inputs {
		for _, ref := range in.To {
			if err := validateEndpointKnown(ref, seenNodes, seenUses); err != nil {
				return Group{}, fmt.Errorf("group %q: export input %q: %w", g.Name, in.Name, err)
			}
		}
	}
	for _, out := range g.Export.Outputs {
		if err := validateEndpointKnown(out.From, seenNodes, seenUses); err != nil {
			return Group{}, fmt.Errorf("group %q: export output %q: %w", g.Name, out.Name, err)
		}
	}

	return g, nil
}

// parseUseBlock parses a group instantiation: `use "<group-name>" { as = "<instance>", source = "<dir>" }`.
func parseUseBlock(block *hcl.Block) (Use, error) {
	content, diags := block.Body.Content(useSchema)
	if diags.HasErrors() {
		return Use{}, fmt.Errorf("%s: %s", block.DefRange, diags.Error())
	}

	asAttr := content.Attributes["as"]
	asVal, diags := asAttr.Expr.Value(nil)
	if diags.HasErrors() || asVal.Type() != cty.String {
		return Use{}, fmt.Errorf("%s: as must be a literal string", asAttr.Range)
	}
	if err := validateName("use instance", asVal.AsString(), asAttr.Range); err != nil {
		return Use{}, err
	}

	sourceAttr := content.Attributes["source"]
	sourceVal, diags := sourceAttr.Expr.Value(nil)
	if diags.HasErrors() || sourceVal.Type() != cty.String {
		return Use{}, fmt.Errorf("%s: source must be a literal string", sourceAttr.Range)
	}

	return Use{
		GroupName: block.Labels[0],
		As:        asVal.AsString(),
		Source:    sourceVal.AsString(),
	}, nil
}

// parseExportBlock parses a group's public interface: which internal ports are reachable from outside the group instance, and under what external name.
func parseExportBlock(block *hcl.Block) (Export, error) {
	content, diags := block.Body.Content(exportBodySchema)
	if diags.HasErrors() {
		return Export{}, fmt.Errorf("%s: %s", block.DefRange, diags.Error())
	}

	var exp Export
	seenInputs := map[string]bool{}
	seenOutputs := map[string]bool{}

	for _, b := range content.Blocks {
		switch b.Type {
		case "input":
			name := b.Labels[0]
			if seenInputs[name] {
				return Export{}, fmt.Errorf("%s: duplicate export input %q", b.DefRange, name)
			}
			seenInputs[name] = true

			ic, diags := b.Body.Content(exportInputSchema)
			if diags.HasErrors() {
				return Export{}, fmt.Errorf("%s: %s", b.DefRange, diags.Error())
			}
			toAttr := ic.Attributes["to"]
			refs, err := parsePortRefList(toAttr)
			if err != nil {
				return Export{}, err
			}
			for _, ref := range refs {
				if ref.Kind != PortInput {
					return Export{}, fmt.Errorf("%s: export input %q must map to input port(s), got %s", toAttr.Range, name, ref)
				}
			}
			exp.Inputs = append(exp.Inputs, ExportInput{Name: name, To: refs})
		case "output":
			name := b.Labels[0]
			if seenOutputs[name] {
				return Export{}, fmt.Errorf("%s: duplicate export output %q", b.DefRange, name)
			}
			seenOutputs[name] = true

			oc, diags := b.Body.Content(exportOutputSchema)
			if diags.HasErrors() {
				return Export{}, fmt.Errorf("%s: %s", b.DefRange, diags.Error())
			}
			fromAttr := oc.Attributes["from"]
			ref, err := parseNodeOrPortRef(fromAttr)
			if err != nil {
				return Export{}, err
			}
			if !ref.IsPort() || ref.Kind != PortOutput {
				return Export{}, fmt.Errorf("%s: export output %q must map to an output port, got %s", fromAttr.Range, name, ref)
			}
			exp.Outputs = append(exp.Outputs, ExportOutput{Name: name, From: ref})
		}
	}

	return exp, nil
}
