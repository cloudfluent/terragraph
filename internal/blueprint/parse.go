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
		{Type: "runtime", LabelNames: []string{"name"}},
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

var runtimeSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "binary", Required: true},
		{Name: "version", Required: false},
		{Name: "default", Required: false},
	},
}

var nodeSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "source", Required: true},
		{Name: "backend_config", Required: false},
		{Name: "vars", Required: false},
		{Name: "runtime", Required: false},
		{Name: "env", Required: false},
		{Name: "approve", Required: false},
	},
}

var edgeSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "from", Required: true},
		{Name: "to", Required: true},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "input", LabelNames: []string{"name"}},
	},
}

var edgeInputSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "from", Required: true}},
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
		{Name: "runtime", Required: false},
		{Name: "env", Required: false},
		{Name: "vars", Required: false},
		{Name: "approve", Required: false},
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
	seenRuntimes := map[string]bool{}

	if err := parseOneFile(path, bp, seenNodes, seenGroups, seenUses, seenRuntimes); err != nil {
		return nil, err
	}
	if err := validateEdges(bp, seenNodes, seenUses); err != nil {
		return nil, err
	}
	if err := validateRuntimes(bp); err != nil {
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
	seenRuntimes := map[string]bool{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".hcl") {
			continue
		}
		if err := parseOneFile(filepath.Join(dir, e.Name()), bp, seenNodes, seenGroups, seenUses, seenRuntimes); err != nil {
			return nil, err
		}
	}

	if err := validateEdges(bp, seenNodes, seenUses); err != nil {
		return nil, err
	}
	if err := validateRuntimes(bp); err != nil {
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

// parseOneFile parses one HCL file and merges its node/edge/group/use/vendor/runtime blocks into bp, checking name and vendor/tfvars-block uniqueness against the caller-supplied seen-sets (shared across every file being merged into the same bp). Edge endpoint and runtime-reference validation deliberately does not happen here: it happens once, in validateEdges/validateRuntimes, after every file that will ever contribute to bp has been merged, so a reference in one file may target a node, use instance, or runtime declared in another.
func parseOneFile(path string, bp *Blueprint, seenNodes, seenGroups, seenUses, seenRuntimes map[string]bool) error {
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
			edges, err := parseEdgeBlock(block)
			if err != nil {
				return err
			}
			bp.Edges = append(bp.Edges, edges...)
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
		case "runtime":
			rt, err := parseRuntimeBlock(block)
			if err != nil {
				return err
			}
			if seenRuntimes[rt.Name] {
				return fmt.Errorf("%s: duplicate runtime %q", block.DefRange, rt.Name)
			}
			seenRuntimes[rt.Name] = true
			bp.Runtimes = append(bp.Runtimes, rt)
		}
	}

	return nil
}

// validateRuntimes checks every Node.Runtime/Use.Runtime reference declared anywhere in this parse scope (including inside a group body: a group's own internal nodes/uses resolve runtime names against this same scope, its own source directory, never the outer blueprint that instantiates it, exactly like a node reference itself is scoped) against bp.Runtimes, and rejects more than one Runtime marking itself Default within this scope.
func validateRuntimes(bp *Blueprint) error {
	defaults := 0
	for _, rt := range bp.Runtimes {
		if rt.Default {
			defaults++
		}
	}
	if defaults > 1 {
		return fmt.Errorf("blueprint declares %d runtime blocks with default = true; at most one is allowed", defaults)
	}

	checkRef := func(kind, owner, ref string) error {
		if ref == "" {
			return nil
		}
		if _, ok := bp.RuntimeByName(ref); !ok {
			return fmt.Errorf("%s %q: runtime.%s is not declared in this blueprint", kind, owner, ref)
		}
		return nil
	}

	for _, n := range bp.Nodes {
		if err := checkRef("node", n.Name, n.Runtime); err != nil {
			return err
		}
	}
	for _, u := range bp.Uses {
		if err := checkRef("use", u.As, u.Runtime); err != nil {
			return err
		}
	}
	for _, g := range bp.Groups {
		for _, n := range g.Nodes {
			if err := checkRef("node", g.Name+"."+n.Name, n.Runtime); err != nil {
				return err
			}
		}
		for _, u := range g.Uses {
			if err := checkRef("use", g.Name+"."+u.As, u.Runtime); err != nil {
				return err
			}
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

// parseRuntimeBlock parses a `runtime "<name>" { }` block (see Runtime).
func parseRuntimeBlock(block *hcl.Block) (Runtime, error) {
	content, diags := block.Body.Content(runtimeSchema)
	if diags.HasErrors() {
		return Runtime{}, fmt.Errorf("%s: %s", block.DefRange, diags.Error())
	}
	if err := validateName("runtime", block.Labels[0], block.DefRange); err != nil {
		return Runtime{}, err
	}

	binaryAttr := content.Attributes["binary"]
	binaryVal, diags := binaryAttr.Expr.Value(nil)
	if diags.HasErrors() || binaryVal.Type() != cty.String {
		return Runtime{}, fmt.Errorf("%s: binary must be a literal string", binaryAttr.Range)
	}
	if binaryVal.AsString() == "" {
		return Runtime{}, fmt.Errorf("%s: binary must not be empty", binaryAttr.Range)
	}

	rt := Runtime{Name: block.Labels[0], Binary: binaryVal.AsString()}

	if attr, ok := content.Attributes["version"]; ok {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() || val.Type() != cty.String {
			return Runtime{}, fmt.Errorf("%s: version must be a literal string", attr.Range)
		}
		rt.Version = val.AsString()
	}

	if attr, ok := content.Attributes["default"]; ok {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() || val.Type() != cty.Bool {
			return Runtime{}, fmt.Errorf("%s: default must be a literal bool", attr.Range)
		}
		rt.Default = val.True()
	}

	return rt, nil
}

// parseRuntimeRef evaluates the optional runtime attribute on a node or use block: a reference of the form runtime.<name> (see Runtime), naming a `runtime` block declared elsewhere in this same parse scope. Returns "" if attr is nil (the attribute was absent).
func parseRuntimeRef(attr *hcl.Attribute) (string, error) {
	if attr == nil {
		return "", nil
	}

	traversal, diags := hcl.AbsTraversalForExpr(attr.Expr)
	if diags.HasErrors() || len(traversal) != 2 {
		return "", fmt.Errorf("%s: runtime must be a reference of the form runtime.<name>", attr.Range)
	}
	root, ok := traversal[0].(hcl.TraverseRoot)
	if !ok || root.Name != "runtime" {
		return "", fmt.Errorf("%s: runtime must be a reference of the form runtime.<name>", attr.Range)
	}
	name, err := traverseAttrName(traversal[1], attr.Range)
	if err != nil {
		return "", err
	}
	return name, nil
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

	runtime, err := parseRuntimeRef(content.Attributes["runtime"])
	if err != nil {
		return Node{}, err
	}

	var env map[string]string
	if attr, ok := content.Attributes["env"]; ok {
		var err error
		env, err = parseEnvAttr(attr)
		if err != nil {
			return Node{}, err
		}
	}

	approve, err := parseApproveAttr(content.Attributes["approve"])
	if err != nil {
		return Node{}, err
	}

	return Node{
		Name:          block.Labels[0],
		Source:        val.AsString(),
		BackendConfig: backendConfig,
		Vars:          vars,
		Runtime:       runtime,
		Env:           env,
		Approve:       approve,
	}, nil
}

// parseApproveAttr evaluates the optional approve attribute, a bare literal string naming one of the levels (see Approve). Unset yields "", which means "inherit"; every layer that can supply one is applied later, in engine.
func parseApproveAttr(attr *hcl.Attribute) (Approve, error) {
	if attr == nil {
		return "", nil
	}
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return "", fmt.Errorf("%s: approve must be a literal string: %s", attr.Range, diags.Error())
	}
	if val.Type() != cty.String {
		return "", fmt.Errorf("%s: approve must be a string", attr.Range)
	}
	a, err := ParseApprove(val.AsString())
	if err != nil {
		return "", fmt.Errorf("%s: %w", attr.Range, err)
	}
	return a, nil
}

// parseVarsAttr evaluates the optional vars attribute: literal input values declared on a node or a use. On a node the keys are module variable names (see Node.Vars); on a use they are the group's export input names (see Use.Vars). The HCL shape is the same either way. The expression is evaluated with no variables or functions in scope, so a reference to another node's or use instance's output (e.g. node.vpc.output.vpc_id) fails to parse here exactly as intended: that kind of value must come from a real edge, not vars, since only an edge records the dependency the engine needs to sequence execution and wait for the value to actually exist.
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
	return parseStringMapAttr(attr, "backend_config")
}

// parseEnvAttr evaluates the optional env attribute (see Node.Env/Use.Env), an object/map of environment variable name to value.
func parseEnvAttr(attr *hcl.Attribute) (map[string]string, error) {
	return parseStringMapAttr(attr, "env")
}

// parseStringMapAttr evaluates attr as an object/map of string keys to string values, the shape shared by backend_config and env. attrName only labels error messages with which attribute failed.
func parseStringMapAttr(attr *hcl.Attribute, attrName string) (map[string]string, error) {
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s: %s must be a literal map of strings: %s", attr.Range, attrName, diags.Error())
	}
	if !val.CanIterateElements() {
		return nil, fmt.Errorf("%s: %s must be a map/object of strings", attr.Range, attrName)
	}

	result := make(map[string]string)
	it := val.ElementIterator()
	for it.Next() {
		k, v := it.Element()
		str, err := convert.Convert(v, cty.String)
		if err != nil {
			return nil, fmt.Errorf("%s: %s.%s must be a string: %s", attr.Range, attrName, k.AsString(), err)
		}
		if str.IsNull() {
			return nil, fmt.Errorf("%s: %s.%s must not be null", attr.Range, attrName, k.AsString())
		}
		result[k.AsString()] = str.AsString()
	}
	return result, nil
}

// parseEdgeBlock parses one `edge { }` block into the edges it declares: exactly one, unless the block carries nested `input` blocks (see parseEdgeInputs), in which case it declares one data edge per nested block. That expansion happens here, at parse time, so nothing downstream ever sees the shorthand: by the time an edge reaches graph.Build it is an ordinary pair of PortRefs, and every later rule (one data edge per input, group export rewriting, cycle detection) applies to the expanded edges without knowing how they were spelled.
func parseEdgeBlock(block *hcl.Block) ([]Edge, error) {
	content, diags := block.Body.Content(edgeSchema)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s: %s", block.DefRange, diags.Error())
	}

	from, err := parseNodeOrPortRef(content.Attributes["from"])
	if err != nil {
		return nil, err
	}
	to, err := parseNodeOrPortRef(content.Attributes["to"])
	if err != nil {
		return nil, err
	}

	if inputs := content.Blocks.OfType("input"); len(inputs) > 0 {
		return parseEdgeInputs(block, inputs, from, to)
	}

	// Either both endpoints name a specific port (explicit data edge) or neither does (implicit, ordering-only edge). Mixing the two would be ambiguous: a value with nowhere declared to land, or an input with no declared source.
	if from.IsPort() != to.IsPort() {
		return nil, fmt.Errorf(
			"%s: \"from\" and \"to\" must both reference specific ports (node.<name>.output.<attr> / node.<name>.input.<attr>) for a data edge, or both reference bare nodes (node.<name>) for an ordering-only edge; got %s -> %s",
			block.DefRange, from, to,
		)
	}

	if from.IsPort() {
		if from.Kind != PortOutput {
			return nil, fmt.Errorf("%s: \"from\" must reference an output port (node.<name>.output.<attr>), got %s", content.Attributes["from"].Range, from)
		}
		if to.Kind != PortInput {
			return nil, fmt.Errorf("%s: \"to\" must reference an input port (node.<name>.input.<attr>), got %s", content.Attributes["to"].Range, to)
		}
	}

	return []Edge{{From: from, To: to}}, nil
}

// parseEdgeInputs expands an edge's nested `input "<var>" { from = output.<attr> }` blocks into one data edge each, between the same pair of endpoints the enclosing edge names. The shape deliberately mirrors a group's export input: a block label naming the destination input, and one attribute saying where the value comes from. Its purpose is only to stop a pair of modules that already expose many flat variables from needing one near-identical `edge` block per variable; nothing about the resulting graph differs from having written them out.
//
// Both endpoints must be bare references, since each nested block supplies the port names itself: an edge that already names a specific port on either side has exactly one value to carry, and there would be nothing for a second input block to mean.
func parseEdgeInputs(block *hcl.Block, inputs hcl.Blocks, from, to PortRef) ([]Edge, error) {
	if from.IsPort() || to.IsPort() {
		return nil, fmt.Errorf(
			"%s: an edge with nested \"input\" blocks must reference bare nodes on both sides (node.<name> / use.<name>); each block's label and \"from\" name the ports, got %s -> %s",
			block.DefRange, from, to,
		)
	}

	edges := make([]Edge, 0, len(inputs))
	seen := map[string]bool{}
	for _, b := range inputs {
		name := b.Labels[0]
		if seen[name] {
			return nil, fmt.Errorf("%s: duplicate input %q on this edge", b.DefRange, name)
		}
		seen[name] = true

		ic, diags := b.Body.Content(edgeInputSchema)
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s: %s", b.DefRange, diags.Error())
		}
		output, err := parseRelativeOutputRef(ic.Attributes["from"])
		if err != nil {
			return nil, err
		}

		edges = append(edges, Edge{
			From: PortRef{Entity: from.Entity, Node: from.Node, Kind: PortOutput, Name: output},
			To:   PortRef{Entity: to.Entity, Node: to.Node, Kind: PortInput, Name: name},
		})
	}
	return edges, nil
}

// parseRelativeOutputRef reads the `from = output.<attr>` attribute of an edge's nested input block and returns just the attribute name. The reference is relative on purpose: the enclosing edge already names exactly one source, so repeating it on every nested block would add a second place for it to disagree with itself. An absolute reference is rejected rather than accepted-if-it-matches, so there is only ever one spelling to read.
func parseRelativeOutputRef(attr *hcl.Attribute) (string, error) {
	traversal, diags := hcl.AbsTraversalForExpr(attr.Expr)
	if diags.HasErrors() {
		return "", fmt.Errorf("%s: must be a reference of the form output.<attr>, not an expression", attr.Range)
	}

	root, ok := traversal[0].(hcl.TraverseRoot)
	if !ok {
		return "", fmt.Errorf("%s: must be a reference of the form output.<attr>", attr.Range)
	}
	if root.Name == "node" || root.Name == "use" {
		return "", fmt.Errorf("%s: must be a relative reference of the form output.<attr>; the source node is the edge's own \"from\", not repeated here", attr.Range)
	}
	if root.Name != string(PortOutput) {
		return "", fmt.Errorf("%s: must be a reference of the form output.<attr>, got %q", attr.Range, root.Name)
	}
	if len(traversal) != 2 {
		return "", fmt.Errorf("%s: expected output.<attr>, got %d path segments", attr.Range, len(traversal))
	}

	return traverseAttrName(traversal[1], attr.Range)
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
	if len(traversal) < 2 {
		return PortRef{}, fmt.Errorf("%s: expected <keyword>.<name>", attr.Range)
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
			edges, err := parseEdgeBlock(b)
			if err != nil {
				return Group{}, err
			}
			g.Edges = append(g.Edges, edges...)
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

	runtime, err := parseRuntimeRef(content.Attributes["runtime"])
	if err != nil {
		return Use{}, err
	}

	var env map[string]string
	if attr, ok := content.Attributes["env"]; ok {
		var err error
		env, err = parseEnvAttr(attr)
		if err != nil {
			return Use{}, err
		}
	}

	var vars map[string]any
	if attr, ok := content.Attributes["vars"]; ok {
		var err error
		vars, err = parseVarsAttr(attr)
		if err != nil {
			return Use{}, err
		}
	}

	approve, err := parseApproveAttr(content.Attributes["approve"])
	if err != nil {
		return Use{}, err
	}

	return Use{
		GroupName: block.Labels[0],
		As:        asVal.AsString(),
		Source:    sourceVal.AsString(),
		Runtime:   runtime,
		Env:       env,
		Vars:      vars,
		Approve:   approve,
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
