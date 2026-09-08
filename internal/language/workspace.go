// Package language provides the editor-facing, tolerant view of a Blueprint
// workspace. Unlike blueprint.ParseFile it accepts incomplete documents so it
// remains useful while a user is typing.
package language

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

// Completion is an editor-neutral suggestion. Start and End are byte offsets
// in the document and identify the expression fragment to replace.
type Completion struct {
	Label         string
	Insert        string
	Detail        string
	Documentation string
	Start         int
	End           int
}

// Location identifies a byte range in a workspace file.
type Location struct {
	Path       string
	Start, End int
}

// Diagnostic is an editor-neutral error range and message.
type Diagnostic struct {
	Start, End int
	Message    string
}

// Workspace is the language Module. Its small Interface is document overlays
// plus completion; the HCL recovery and Terraform inspection implementation is
// deliberately kept behind it.
type Workspace struct {
	mu        sync.RWMutex
	root      string
	documents map[string][]byte
}

func NewWorkspace(root string) *Workspace {
	return &Workspace{root: root, documents: make(map[string][]byte)}
}

func (w *Workspace) SetRoot(root string) { w.mu.Lock(); w.root = root; w.mu.Unlock() }

func (w *Workspace) SetDocument(path string, text []byte) {
	path = absolute(path)
	w.mu.Lock()
	w.documents[path] = append([]byte(nil), text...)
	w.mu.Unlock()
}

func (w *Workspace) CloseDocument(path string) {
	w.mu.Lock()
	delete(w.documents, absolute(path))
	w.mu.Unlock()
}

// Complete returns suggestions for the document offset, even when HCL has
// syntax diagnostics. Context is reserved for future cancellable module reads.
func (w *Workspace) Complete(_ context.Context, path string, offset int) []Completion {
	path = absolute(path)
	text := w.document(path)
	if offset < 0 || offset > len(text) {
		return nil
	}

	model := w.model(path, text).at(text, offset)
	blocks := blockPathAt(text, offset)
	if labelStart, ok := edgeInputLabelAt(text, offset, blocks); ok {
		return edgeInputLabelCompletions(model, text, labelStart, offset)
	}
	if literalAt(text, offset) {
		return nil
	}
	start := traversalStart(text, offset)
	fragment := string(text[start:offset])
	objectAttribute, insideObject := objectAttributeAt(text, offset)
	if objectAttribute == "vars" && !strings.Contains(fragment, ".") {
		p, _ := varsPortsAt(model, text, offset)
		return propertyCompletions(p, fragment, start, offset)
	}
	if insideObject {
		return nil
	}
	if strings.HasPrefix(fragment, "runtime.") {
		return runtimeCompletions(model, fragment, start, offset)
	}
	if strings.HasPrefix(fragment, "node.") || strings.HasPrefix(fragment, "use.") {
		return traversalCompletions(model, fragment, directionAt(text, offset), start, offset)
	}
	if isEdgeInput(blocks) && directionAt(text, offset) == "from" {
		return relativeOutputCompletions(model, text, fragment, start, offset)
	}
	return contextCompletions(blocks, fragment, start, offset)
}

type workspaceModel struct {
	nodes    map[string]ports
	uses     map[string]ports
	runtimes []string
	groups   map[string]*workspaceModel
}
type ports struct {
	// Unknown schemas must not turn unreadable or unvendored sources into false unknown-port errors.
	known                   bool
	inputs, outputs         []string
	inputsMeta, outputsMeta map[string]portMeta
}

type portMeta struct {
	typeName, description, deprecated string
	sensitive, required               bool
}

func (w *Workspace) model(path string, text []byte) workspaceModel {
	m := newWorkspaceModel()
	files := w.blueprintFiles(path)
	bodies := make(map[string]*hclsyntax.Body, len(files))
	runtimes := make(map[string]module.FileMode)
	defaultMode, defaults := module.TerraformFiles, 0
	// Runtime declarations may follow their nodes or live in an unsaved sibling, so collect them before inspecting any source.
	for _, candidate := range files {
		contents := w.document(candidate)
		if candidate == path {
			contents = text
		}
		file, _ := hclsyntax.ParseConfig(contents, candidate, hcl.InitialPos)
		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		bodies[candidate] = body
		for _, block := range body.Blocks {
			if block.Type != "runtime" || len(block.Labels) != 1 {
				continue
			}
			mode := module.FileModeForBinary(literalAttribute(block, "binary"))
			runtimes[block.Labels[0]] = mode
			if attr := block.Body.Attributes["default"]; attr != nil {
				value, diags := attr.Expr.Value(nil)
				if !diags.HasErrors() && value.IsKnown() && !value.IsNull() && value.Type() == cty.Bool && value.True() {
					defaultMode = mode
					defaults++
				}
			}
		}
	}
	if defaults > 1 {
		defaultMode = module.UnknownFiles
	}
	for _, candidate := range files {
		if body := bodies[candidate]; body != nil {
			w.addBodyToModel(&m, candidate, body, runtimes, defaultMode)
		}
	}
	m.runtimes = uniqueSorted(m.runtimes)
	return m
}

func newWorkspaceModel() workspaceModel {
	return workspaceModel{nodes: map[string]ports{}, uses: map[string]ports{}, groups: map[string]*workspaceModel{}}
}

// Groups own their node namespace while runtime declarations remain directory-scoped.
func (m workspaceModel) at(text []byte, offset int) workspaceModel {
	for _, block := range openBlocksAt(text, offset) {
		if block.name == "group" {
			if group := m.groups[block.label]; group != nil {
				scope := *group
				scope.runtimes = m.runtimes
				return scope
			}
			return newWorkspaceModel()
		}
	}
	return m
}

func (w *Workspace) addBodyToModel(m *workspaceModel, path string, body *hclsyntax.Body, runtimes map[string]module.FileMode, fallback module.FileMode) {
	for _, block := range body.Blocks {
		switch block.Type {
		case "group":
			if len(block.Labels) == 1 {
				group := newWorkspaceModel()
				// A group inherits its runtime at the use site, never from a default-marked declaration in its source directory.
				w.addBodyToModel(&group, path, block.Body, runtimes, module.UnknownFiles)
				m.groups[block.Labels[0]] = &group
			}
		case "node":
			if len(block.Labels) != 1 {
				continue
			}
			if source := literalAttribute(block, "source"); source != "" {
				m.nodes[block.Labels[0]] = inspectPorts(filepath.Dir(path), source, nodeFileMode(block, runtimes, fallback))
			} else {
				m.nodes[block.Labels[0]] = ports{}
			}
		case "use":
			if len(block.Labels) != 1 {
				continue
			}
			as, source := literalAttribute(block, "as"), literalAttribute(block, "source")
			if as != "" && source != "" {
				m.uses[as] = w.inspectGroupPorts(filepath.Dir(path), source, block.Labels[0])
			}
		case "runtime":
			if len(block.Labels) == 1 {
				m.runtimes = append(m.runtimes, block.Labels[0])
			}
		}
	}
}

func nodeFileMode(block *hclsyntax.Block, runtimes map[string]module.FileMode, fallback module.FileMode) module.FileMode {
	attr := block.Body.Attributes["runtime"]
	if attr == nil {
		return fallback
	}
	traversal, diags := hcl.AbsTraversalForExpr(attr.Expr)
	if !diags.HasErrors() && len(traversal) == 2 && traversal.RootName() == "runtime" {
		if name, ok := traversal[1].(hcl.TraverseAttr); ok {
			if mode, exists := runtimes[name.Name]; exists {
				return mode
			}
		}
	}
	return module.UnknownFiles
}

func inspectPorts(base, source string, mode module.FileMode) ports {
	if blueprint.IsRemote(source) {
		return ports{}
	}
	schema, err := module.Inspect(filepath.Join(base, source), mode)
	if err != nil {
		return ports{}
	}
	p := ports{known: true, inputsMeta: map[string]portMeta{}, outputsMeta: map[string]portMeta{}}
	for name, variable := range schema.Variables {
		p.inputs = append(p.inputs, name)
		p.inputsMeta[name] = portMeta{typeName: variable.Type, description: variable.Description, deprecated: variable.Deprecated, sensitive: variable.Sensitive, required: variable.Required}
	}
	for name, output := range schema.OutputDetails {
		p.outputs = append(p.outputs, name)
		p.outputsMeta[name] = portMeta{typeName: output.Type, description: output.Description, deprecated: output.Deprecated, sensitive: output.Sensitive}
	}
	sort.Strings(p.inputs)
	sort.Strings(p.outputs)
	return p
}

func (w *Workspace) inspectGroupPorts(base, source, groupName string) ports {
	if blueprint.IsRemote(source) {
		return ports{}
	}
	dir := filepath.Join(base, source)
	p := ports{inputsMeta: map[string]portMeta{}, outputsMeta: map[string]portMeta{}}
	for _, candidate := range w.hclFiles(dir) {
		contents := w.document(candidate)
		file, diags := hclsyntax.ParseConfig(contents, candidate, hcl.InitialPos)
		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, block := range body.Blocks {
			if block.Type != "group" || len(block.Labels) != 1 || block.Labels[0] != groupName {
				continue
			}
			p.known = !diags.HasErrors()
			for _, export := range block.Body.Blocks {
				if export.Type != "export" {
					continue
				}
				// A group's exposed ports are the labels of the export block's own input/output blocks, the same shape blueprint.parseExportBlock reads.
				for _, port := range export.Body.Blocks {
					if len(port.Labels) != 1 {
						continue
					}
					switch port.Type {
					case "input":
						p.inputs = append(p.inputs, port.Labels[0])
					case "output":
						p.outputs = append(p.outputs, port.Labels[0])
					}
				}
			}
		}
	}
	p.inputs = uniqueSorted(p.inputs)
	p.outputs = uniqueSorted(p.outputs)
	return p
}

func literalAttribute(block *hclsyntax.Block, name string) string {
	attr := block.Body.Attributes[name]
	if attr == nil {
		return ""
	}
	value, diags := attr.Expr.Value(nil)
	if diags.HasErrors() || !value.IsKnown() || value.IsNull() || value.Type() != ctyString {
		return ""
	}
	return value.AsString()
}

var ctyString = cty.String

func traversalCompletions(m workspaceModel, fragment, direction string, start, end int) []Completion {
	parts := strings.Split(fragment, ".")
	if len(parts) == 1 {
		return nil
	}
	entities := m.nodes
	noun := "node"
	if parts[0] == "use" {
		entities = m.uses
		noun = "group"
	}
	if len(parts) == 2 {
		return namedCompletions(entities, parts[1], func(name string) Completion {
			return Completion{Label: name, Insert: parts[0] + "." + name, Detail: noun, Start: start, End: end}
		})
	}
	p, exists := entities[parts[1]]
	if !exists {
		return nil
	}
	if len(parts) == 3 {
		kinds := []string{"output", "input"}
		if direction == "from" {
			kinds = []string{"output"}
		}
		if direction == "to" {
			kinds = []string{"input"}
		}
		return stringCompletions(kinds, parts[2], func(kind string) Completion {
			return Completion{Label: kind, Insert: parts[0] + "." + parts[1] + "." + kind, Detail: "port kind", Start: start, End: end}
		})
	}
	if len(parts) == 4 && (parts[2] == "input" || parts[2] == "output") {
		names := p.inputs
		if parts[2] == "output" {
			names = p.outputs
		}
		return stringCompletions(names, parts[3], func(name string) Completion {
			meta := p.inputsMeta[name]
			if parts[2] == "output" {
				meta = p.outputsMeta[name]
			}
			return portCompletion(name, strings.Join(parts[:3], ".")+"."+name, meta, parts[2] == "output", start, end)
		})
	}
	return nil
}

func propertyCompletions(p ports, prefix string, start, end int) []Completion {
	return stringCompletions(p.inputs, prefix, func(name string) Completion {
		return portCompletion(name, name, p.inputsMeta[name], false, start, end)
	})
}

type attributeSpec struct {
	name, insert, detail, documentation string
}

var completionSchemas = map[string][]attributeSpec{
	"": {
		{name: "node", insert: "node \"name\" {\n  source = \"\"\n}", detail: "Blueprint block", documentation: "Declares one Terraform or OpenTofu module in the graph."},
		{name: "edge", insert: "edge {\n  from = node.source.output.value\n  to   = node.target.input.value\n}", detail: "Blueprint block", documentation: "Connects a source node output to a target node input."},
		{name: "runtime", insert: "runtime \"name\" {\n  binary = \"tofu\"\n}", detail: "Blueprint block", documentation: "Declares a reusable Terraform or OpenTofu runtime."},
		{name: "group", insert: "group \"name\" {\n}", detail: "Blueprint block", documentation: "Declares a reusable sub-blueprint."},
		{name: "use", insert: "use \"group\" {\n  as     = \"name\"\n  source = \"\"\n}", detail: "Blueprint block", documentation: "Instantiates a reusable group."},
		{name: "vendor", insert: "vendor {\n}", detail: "Blueprint block", documentation: "Configures the local vendor directory."},
		{name: "tfvars", insert: "tfvars {\n}", detail: "Blueprint block", documentation: "Configures where resolved input values are written."},
		{name: "lock", insert: "lock {\n  s3 {\n    bucket = \"\"\n    key    = \"\"\n    region = \"\"\n  }\n}", detail: "Blueprint block", documentation: "Serializes plan/apply/destroy across machines with a remote lock object."},
		{name: "snapshots", insert: "snapshots { }", detail: "Blueprint block", documentation: "Opts the graph into local output snapshots, consumed as the input source of last resort."},
	},
	"group": {
		{name: "node", insert: "node \"name\" {\n  source = \"\"\n}", detail: "Group block"},
		{name: "edge", insert: "edge {\n  from = node.\n  to = node.\n}", detail: "Group block"},
		{name: "use", insert: "use \"group\" {\n  as = \"name\"\n  source = \"\"\n}", detail: "Group block"},
		{name: "export", insert: "export {\n}", detail: "Group interface"},
	},
	"export": {
		{name: "input", insert: "input \"name\" {\n  to = node.\n}", detail: "Export input"},
		{name: "output", insert: "output \"name\" {\n  from = node.\n}", detail: "Export output"},
	},
	"export.input":  {{name: "to", insert: "to = node.", detail: "required input reference"}},
	"export.output": {{name: "from", insert: "from = node.", detail: "required output reference"}},
	"snapshots":     {},
	"node": {
		{name: "source", insert: "source = \"\"", detail: "required string", documentation: "Path or remote source of the Terraform or OpenTofu module."},
		{name: "vars", insert: "vars = {\n}", detail: "object", documentation: "Literal Terraform input values. Use an edge for another node's output."},
		{name: "env", insert: "env = {\n}", detail: "map(string)", documentation: "Extra environment variables for this module's Terraform or OpenTofu process."},
		{name: "runtime", insert: "runtime = runtime.", detail: "runtime reference", documentation: "Selects a declared runtime for this node."},
		{name: "backend_config", insert: "backend_config = {\n}", detail: "map(string)", documentation: "Backend configuration passed to terraform init."},
		{name: "approve", insert: "approve = \"\"", detail: "optional string", documentation: "How much of this node's plan may be applied: \"none\", \"safe\" (create/update, the default), or \"all\" (adds replace/delete)."},
	},
	"edge": {
		{name: "from", insert: "from = node.", detail: "required output reference", documentation: "Source node output, or a bare node for an ordering-only edge."},
		{name: "to", insert: "to = node.", detail: "required input reference", documentation: "Target node input, or a bare node for an ordering-only edge."},
		{name: "input", insert: "input \"name\" {\n  from = output.\n}", detail: "Edge block", documentation: "Wires one more output of this edge's from node into an input of its to node. Only on an edge whose from and to are bare references."},
	},
	"edge.input": {
		{name: "from", insert: "from = output.", detail: "required output reference", documentation: "Which output of the edge's own from node feeds this input. Relative: the source node is not repeated here."},
	},
	"runtime": {
		{name: "binary", insert: "binary = \"\"", detail: "required string", documentation: "Terraform or OpenTofu binary path, or a command resolved from PATH."},
		{name: "version", insert: "version = \"\"", detail: "optional string", documentation: "Records which versions this runtime is meant to represent. Never checked against the binary, and has no effect on execution."},
		{name: "default", insert: "default = true", detail: "optional bool", documentation: "Makes this runtime the blueprint-wide fallback."},
	},
	"use": {
		{name: "as", insert: "as = \"\"", detail: "required string", documentation: "Namespace used to refer to this group instance."},
		{name: "source", insert: "source = \"\"", detail: "required string", documentation: "Local path or remote source containing the group."},
		{name: "backend_config", insert: "backend_config = {\n}", detail: "map(string)", documentation: "Backend configuration merged onto every node this instance expands to. Leaf keys win."},
		{name: "runtime", insert: "runtime = runtime.", detail: "runtime reference", documentation: "Default runtime for nodes expanded from this group."},
		{name: "env", insert: "env = {\n}", detail: "map(string)", documentation: "Environment variables inherited by nodes expanded from this group."},
		{name: "vars", insert: "vars = {\n}", detail: "object", documentation: "Literal values for this instance's export inputs. Use an edge for another node's output."},
		{name: "approve", insert: "approve = \"\"", detail: "optional string", documentation: "Default approve level for nodes expanded from this group, unless a node sets its own."},
	},
	"vendor": {
		{name: "directory", insert: "directory = \"vendor\"", detail: "optional string", documentation: "Directory used to store vendored module sources."},
		{name: "manifest_file", insert: "manifest_file = \"vendor.yaml\"", detail: "optional string", documentation: "Vendor manifest filename."},
	},
	"tfvars": {
		{name: "location", insert: "location = \"workdir\"", detail: "optional string", documentation: "Either workdir (default) or module."},
	},
	"lock": {
		{name: "s3", insert: "s3 {\n  bucket = \"\"\n  key    = \"\"\n  region = \"\"\n}", detail: "Lock block", documentation: "S3 lock object via conditional writes. The only nested lock type in this release."},
	},
	"s3": {
		{name: "bucket", insert: "bucket = \"\"", detail: "required string", documentation: "S3 bucket that holds the graph lock object."},
		{name: "key", insert: "key = \"\"", detail: "required string", documentation: "Object key for this graph. Must not be a node's state key."},
		{name: "region", insert: "region = \"\"", detail: "required string", documentation: "AWS region of the bucket."},
	},
	"lock.s3": {
		{name: "bucket", insert: "bucket = \"\"", detail: "required string", documentation: "S3 bucket that holds the graph lock object."},
		{name: "key", insert: "key = \"\"", detail: "required string", documentation: "Object key for this graph. Must not be a node's state key."},
		{name: "region", insert: "region = \"\"", detail: "required string", documentation: "AWS region of the bucket."},
	},
}

// contextCompletions suggests what may be written inside the block containing the cursor. path is that block's chain of enclosing Blueprint blocks (see blockPathAt), matched against completionSchemas by longest suffix, so a nested block picks its own schema ("edge.input") while one that only ever means one thing keeps a single entry wherever it appears ("node", inside a group or not).
func contextCompletions(path []string, prefix string, start, end int) []Completion {
	fields := schemaForPath(path)
	return stringCompletions(specNames(fields), prefix, func(name string) Completion {
		for _, field := range fields {
			if field.name == name {
				return Completion{Label: field.name, Insert: field.insert, Detail: field.detail, Documentation: field.documentation, Start: start, End: end}
			}
		}
		return Completion{}
	})
}

func schemaForPath(path []string) []attributeSpec {
	if len(path) == 0 {
		return completionSchemas[""]
	}
	for i := range path {
		if fields, ok := completionSchemas[strings.Join(path[i:], ".")]; ok {
			return fields
		}
	}
	return nil
}

func specNames(fields []attributeSpec) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.name)
	}
	return names
}

func runtimeCompletions(m workspaceModel, fragment string, start, end int) []Completion {
	parts := strings.Split(fragment, ".")
	if len(parts) != 2 {
		return nil
	}
	return stringCompletions(m.runtimes, parts[1], func(name string) Completion {
		return Completion{Label: name, Insert: "runtime." + name, Detail: "runtime", Start: start, End: end}
	})
}

func portCompletion(name, insert string, meta portMeta, output bool, start, end int) Completion {
	detail := meta.typeName
	if output {
		detail = ""
	} else if detail == "" {
		detail = "input variable"
	}
	tags := []string{}
	if meta.required {
		tags = append(tags, "required")
	}
	if meta.sensitive {
		tags = append(tags, "sensitive")
	}
	if len(tags) > 0 {
		tagText := "(" + strings.Join(tags, ", ") + ")"
		if detail != "" {
			detail += " " + tagText
		} else {
			detail = tagText
		}
	}
	if meta.description != "" {
		if detail != "" {
			detail += " — "
		}
		detail += meta.description
	}
	documentation := meta.description
	if meta.deprecated != "" {
		documentation = strings.TrimSpace(documentation + "\n\nDeprecated: " + meta.deprecated)
	}
	return Completion{Label: name, Insert: insert, Detail: detail, Documentation: documentation, Start: start, End: end}
}
func namedCompletions(values map[string]ports, prefix string, build func(string) Completion) []Completion {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	return stringCompletions(names, prefix, build)
}
func stringCompletions(values []string, prefix string, build func(string) Completion) []Completion {
	result := []Completion{}
	for _, value := range uniqueSorted(values) {
		if strings.HasPrefix(value, prefix) {
			result = append(result, build(value))
		}
	}
	return result
}
func uniqueSorted(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		set[value] = true
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func traversalStart(text []byte, offset int) int {
	start := offset
	for start > 0 && (text[start-1] == '.' || text[start-1] == '_' || text[start-1] == '-' || (text[start-1] >= 'a' && text[start-1] <= 'z') || (text[start-1] >= 'A' && text[start-1] <= 'Z') || (text[start-1] >= '0' && text[start-1] <= '9')) {
		start--
	}
	return start
}

// varsPortsAt returns the ports whose names are valid keys inside the vars object containing offset: a node's module inputs, or a use instance's export inputs.
func varsPortsAt(m workspaceModel, text []byte, offset int) (ports, bool) {
	path := blockPathAt(text, offset)
	if len(path) == 0 {
		return ports{}, false
	}
	switch path[len(path)-1] {
	case "use":
		p, ok := m.uses[useAsAt(text, offset)]
		return p, ok
	case "node":
		p, ok := m.nodes[nodeAt(text, offset)]
		return p, ok
	default:
		return ports{}, false
	}
}

func isEdgeInput(path []string) bool {
	return len(path) >= 2 && path[len(path)-1] == "input" && path[len(path)-2] == "edge"
}

func lookupEntity(m workspaceModel, keyword, name string) (ports, bool) {
	if keyword == "use" {
		p, ok := m.uses[name]
		return p, ok
	}
	p, ok := m.nodes[name]
	return p, ok
}

func edgeInputLabelCompletions(m workspaceModel, text []byte, start, end int) []Completion {
	target, ok := edgeEndpointPorts(m, text, start, "to")
	if !ok {
		return nil
	}
	prefix := string(text[start:end])
	return stringCompletions(target.inputs, prefix, func(name string) Completion {
		return portCompletion(name, name, target.inputsMeta[name], false, start, end)
	})
}

// relativeOutputCompletions completes the `from = output.<attr>` reference of an
// edge's nested input block against the outputs of the node the enclosing edge's
// own from names. The reference carries no node name of its own, so unlike
// traversalCompletions there is nothing in the fragment to resolve it with.
func relativeOutputCompletions(m workspaceModel, text []byte, fragment string, start, end int) []Completion {
	source, ok := edgeEndpointPorts(m, text, start, "from")
	if !ok {
		return nil
	}
	parts := strings.Split(fragment, ".")
	if len(parts) == 1 {
		return stringCompletions([]string{"output"}, parts[0], func(kind string) Completion {
			return Completion{Label: kind, Insert: kind + ".", Detail: "port kind", Start: start, End: end}
		})
	}
	if len(parts) != 2 || parts[0] != "output" {
		return nil
	}
	return stringCompletions(source.outputs, parts[1], func(name string) Completion {
		return portCompletion(name, "output."+name, source.outputsMeta[name], true, start, end)
	})
}

// Definition follows directory loading's filename filter so excluded files cannot supply destinations that execution never reads.
func (w *Workspace) Definition(_ context.Context, path string, offset int) (Location, bool) {
	path = absolute(path)
	text := w.document(path)
	kind, name, ok := referenceAt(text, offset)
	if !ok {
		return Location{}, false
	}
	for _, candidate := range w.blueprintFiles(path) {
		file, _ := hclsyntax.ParseConfig(w.document(candidate), candidate, hcl.InitialPos)
		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		if kind == "node" {
			if group := groupAt(text, offset); group != "" {
				var groupBody *hclsyntax.Body
				for _, block := range body.Blocks {
					if block.Type == "group" && len(block.Labels) == 1 && block.Labels[0] == group {
						groupBody = block.Body
					}
				}
				if groupBody == nil {
					continue
				}
				body = groupBody
			}
		}
		for _, block := range body.Blocks {
			if block.Type != kind || len(block.Labels) != 1 || block.Labels[0] != name || len(block.LabelRanges) == 0 {
				continue
			}
			label := block.LabelRanges[0]
			return Location{Path: candidate, Start: label.Start.Byte, End: label.End.Byte}, true
		}
	}
	return Location{}, false
}

// Diagnose uses recovered expressions so literals and comments never become graph references.
func (w *Workspace) Diagnose(_ context.Context, path string) []Diagnostic {
	path = absolute(path)
	text := w.document(path)
	model := w.model(path, text)
	file, _ := hclsyntax.ParseConfig(text, path, hcl.InitialPos)
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}
	diagnostics := []Diagnostic{}
	walkAttributes(body, func(attr *hclsyntax.Attribute) {
		scope := model.at(text, attr.Range().Start.Byte)
		for _, traversal := range attr.Expr.Variables() {
			diagnostics = append(diagnostics, nodeDiagnostics(scope, attr.Name, traversal)...)
		}
		if attr.Name != "vars" {
			return
		}
		p, exists := varsPortsAt(scope, text, attr.Range().Start.Byte)
		object, ok := attr.Expr.(*hclsyntax.ObjectConsExpr)
		if !exists || !p.known || !ok {
			return
		}
		for _, item := range object.Items {
			value, diags := item.KeyExpr.Value(nil)
			if diags.HasErrors() || !value.IsKnown() || value.IsNull() || value.Type() != cty.String {
				continue
			}
			name := value.AsString()
			if !containsString(p.inputs, name) {
				rng := item.KeyExpr.Range()
				diagnostics = append(diagnostics, Diagnostic{Start: rng.Start.Byte, End: rng.End.Byte, Message: "Unknown input " + name + availableHint(p.inputs)})
			}
		}
	})
	return append(diagnostics, edgeInputDiagnostics(model, body, text)...)
}

func nodeDiagnostics(model workspaceModel, direction string, traversal hcl.Traversal) []Diagnostic {
	if len(traversal) < 2 {
		return nil
	}
	root, rootOK := traversal[0].(hcl.TraverseRoot)
	name, nameOK := traversal[1].(hcl.TraverseAttr)
	if !rootOK || !nameOK || root.Name != "node" {
		return nil
	}
	p, exists := model.nodes[name.Name]
	if !exists {
		return []Diagnostic{{Start: name.SrcRange.Start.Byte, End: name.SrcRange.End.Byte, Message: "Unknown node " + name.Name}}
	}
	if len(traversal) < 3 {
		return nil
	}
	kind, ok := traversal[2].(hcl.TraverseAttr)
	if !ok || (kind.Name != "input" && kind.Name != "output") {
		return nil
	}
	diagnostics := []Diagnostic{}
	if (direction == "from" && kind.Name != "output") || (direction == "to" && kind.Name != "input") {
		expected := "output"
		if direction == "to" {
			expected = "input"
		}
		diagnostics = append(diagnostics, Diagnostic{Start: kind.SrcRange.Start.Byte, End: kind.SrcRange.End.Byte, Message: direction + " must reference node " + expected})
	}
	if len(traversal) < 4 || !p.known {
		return diagnostics
	}
	port, ok := traversal[3].(hcl.TraverseAttr)
	if !ok {
		return diagnostics
	}
	available := p.inputs
	if kind.Name == "output" {
		available = p.outputs
	}
	if !containsString(available, port.Name) {
		diagnostics = append(diagnostics, Diagnostic{Start: port.SrcRange.Start.Byte, End: port.SrcRange.End.Byte, Message: "Unknown " + kind.Name + " " + port.Name + availableHint(available)})
	}
	return diagnostics
}

// Relative edge ports have no entity name, so their containing group's model must resolve both endpoints.
func edgeInputDiagnostics(model workspaceModel, body *hclsyntax.Body, text []byte) []Diagnostic {
	diagnostics := []Diagnostic{}
	for _, block := range body.Blocks {
		if block.Type == "group" {
			scope := model.at(text, block.OpenBraceRange.End.Byte)
			diagnostics = append(diagnostics, edgeInputDiagnostics(scope, block.Body, text)...)
		}
		if block.Type != "edge" {
			continue
		}
		source, hasSource := blockEndpointPorts(model, block, "from")
		target, hasTarget := blockEndpointPorts(model, block, "to")
		for _, input := range block.Body.Blocks {
			if input.Type != "input" || len(input.Labels) != 1 || len(input.LabelRanges) != 1 {
				continue
			}
			if hasTarget && target.known && !containsString(target.inputs, input.Labels[0]) {
				start, end := unquotedRange(text, input.LabelRanges[0])
				diagnostics = append(diagnostics, Diagnostic{Start: start, End: end, Message: "Unknown input " + input.Labels[0] + availableHint(target.inputs)})
			}
			attr := input.Body.Attributes["from"]
			if attr == nil || !hasSource || !source.known {
				continue
			}
			name, ok := relativeOutputName(attr.Expr)
			if ok && !containsString(source.outputs, name) {
				rng := attr.Expr.Range()
				diagnostics = append(diagnostics, Diagnostic{Start: rng.Start.Byte, End: rng.End.Byte, Message: "Unknown output " + name + availableHint(source.outputs)})
			}
		}
	}
	return diagnostics
}

func walkAttributes(body *hclsyntax.Body, visit func(*hclsyntax.Attribute)) {
	names := make([]string, 0, len(body.Attributes))
	for name := range body.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		visit(body.Attributes[name])
	}
	for _, block := range body.Blocks {
		walkAttributes(block.Body, visit)
	}
}

// blockEndpointPorts resolves one edge endpoint attribute to the ports of the
// node or group instance it names, provided it is a bare reference (the only
// form an edge with nested input blocks may use).
func blockEndpointPorts(m workspaceModel, block *hclsyntax.Block, side string) (ports, bool) {
	attr := block.Body.Attributes[side]
	if attr == nil {
		return ports{}, false
	}
	traversal, diags := hcl.AbsTraversalForExpr(attr.Expr)
	if diags.HasErrors() || len(traversal) != 2 {
		return ports{}, false
	}
	root, rootOK := traversal[0].(hcl.TraverseRoot)
	step, stepOK := traversal[1].(hcl.TraverseAttr)
	if !rootOK || !stepOK || (root.Name != "node" && root.Name != "use") {
		return ports{}, false
	}
	return lookupEntity(m, root.Name, step.Name)
}

// relativeOutputName reads an `output.<attr>` reference, the only form an edge's
// nested input block accepts (see blueprint.parseRelativeOutputRef).
func relativeOutputName(expr hcl.Expression) (string, bool) {
	traversal, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() || len(traversal) != 2 {
		return "", false
	}
	root, rootOK := traversal[0].(hcl.TraverseRoot)
	step, stepOK := traversal[1].(hcl.TraverseAttr)
	if !rootOK || !stepOK || root.Name != "output" {
		return "", false
	}
	return step.Name, true
}

// unquotedRange narrows a block label's range to the label itself, whether or
// not the parser included the surrounding quotes.
func unquotedRange(text []byte, rng hcl.Range) (int, int) {
	start, end := rng.Start.Byte, rng.End.Byte
	if start < len(text) && text[start] == '"' {
		start++
	}
	if end > start && end <= len(text) && text[end-1] == '"' {
		end--
	}
	return start, end
}

func availableHint(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return "; available: " + strings.Join(values, ", ")
}

func referenceAt(text []byte, offset int) (string, string, bool) {
	file, _ := hclsyntax.ParseConfig(text, "", hcl.InitialPos)
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return "", "", false
	}
	kind, name := "", ""
	walkAttributes(body, func(attr *hclsyntax.Attribute) {
		for _, traversal := range attr.Expr.Variables() {
			if len(traversal) < 2 {
				continue
			}
			root, rootOK := traversal[0].(hcl.TraverseRoot)
			step, stepOK := traversal[1].(hcl.TraverseAttr)
			if rootOK && stepOK && (root.Name == "node" || root.Name == "runtime") && offset >= step.SrcRange.Start.Byte && offset <= step.SrcRange.End.Byte {
				kind, name = root.Name, step.Name
			}
		}
	})
	return kind, name, kind != ""
}

func (w *Workspace) blueprintFiles(path string) []string {
	files := w.hclFiles(filepath.Dir(path))
	// Explicitly opened non-HCL filenames retain editor support, just as explicit CLI file selection bypasses directory discovery.
	if filepath.Ext(path) != ".hcl" || blueprint.IsBlueprintFilename(filepath.Base(path)) {
		files = append(files, path)
	}
	return uniqueSorted(files)
}

// Open file overlays remain part of their directory even before the first save or after a disk deletion.
func (w *Workspace) hclFiles(dir string) []string {
	entries, _ := os.ReadDir(dir)
	files := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && blueprint.IsBlueprintFilename(entry.Name()) {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	w.mu.RLock()
	for path := range w.documents {
		if filepath.Dir(path) == dir && blueprint.IsBlueprintFilename(filepath.Base(path)) {
			files = append(files, path)
		}
	}
	w.mu.RUnlock()
	return uniqueSorted(files)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (w *Workspace) document(path string) []byte {
	w.mu.RLock()
	text, ok := w.documents[path]
	w.mu.RUnlock()
	if ok {
		return append([]byte(nil), text...)
	}
	text, _ = os.ReadFile(path)
	return text
}

// Document returns the current editor overlay when present, otherwise the
// on-disk contents. It is used to convert definition byte offsets to LSP
// positions in the server adapter.
func (w *Workspace) Document(path string) []byte { return w.document(absolute(path)) }

func absolute(path string) string {
	result, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return result
}
