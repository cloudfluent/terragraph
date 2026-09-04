// Package language provides the editor-facing, tolerant view of a Blueprint
// workspace. Unlike blueprint.ParseFile it accepts incomplete documents so it
// remains useful while a user is typing.
package language

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
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
	if isContractsFile(path) {
		return contractsCompletions(text, offset)
	}

	model := w.model(path, text)
	start := traversalStart(text, offset)
	fragment := string(text[start:offset])
	objectAttribute := objectAttributeAt(text, offset)
	if objectAttribute == "vars" && !strings.Contains(fragment, ".") {
		p, _ := varsPortsAt(model, text, offset)
		return propertyCompletions(p, fragment, start, offset)
	}
	if objectAttribute != "" {
		return nil
	}
	if strings.HasPrefix(fragment, "runtime") {
		return runtimeCompletions(model, fragment, start, offset)
	}
	if strings.HasPrefix(fragment, "node") || strings.HasPrefix(fragment, "use") {
		return traversalCompletions(model, fragment, directionAt(text, offset), start, offset)
	}
	blocks := blockPathAt(text, offset)
	if labelStart, ok := edgeInputLabelAt(text, offset, blocks); ok {
		return edgeInputLabelCompletions(model, text, labelStart, offset)
	}
	if isEdgeInput(blocks) && directionAt(text, offset) == "from" {
		return relativeOutputCompletions(model, text, fragment, start, offset)
	}
	return contextCompletions(blocks, fragment, start, offset)
}

// isContractsFile identifies the reserved contracts sibling (see blueprint.ParseContracts): such files speak the contract grammar, not the blueprint one, so completion and diagnostics route to their own rules.
func isContractsFile(path string) bool {
	return filepath.Base(path) == "contracts.hcl"
}

// contractsCompletions serves contracts.hcl from its own schema; the blueprint workspace model does not apply there.
func contractsCompletions(text []byte, offset int) []Completion {
	blocks := blockPathAt(text, offset)
	start := traversalStart(text, offset)
	return contextCompletionsIn(contractsCompletionSchemas, blocks, string(text[start:offset]), start, offset)
}

type workspaceModel struct {
	nodes    map[string]ports
	uses     map[string]ports
	runtimes []string
}
type ports struct {
	inputs, outputs         []string
	inputsMeta, outputsMeta map[string]portMeta
}

type portMeta struct {
	typeName, description, deprecated string
	sensitive, required               bool
}

func (w *Workspace) model(path string, text []byte) workspaceModel {
	m := workspaceModel{nodes: map[string]ports{}, uses: map[string]ports{}}
	for _, candidate := range w.blueprintFiles(path) {
		contents := w.document(candidate)
		if candidate == path {
			contents = text
		}
		w.addFileToModel(&m, candidate, contents)
	}
	m.runtimes = uniqueSorted(m.runtimes)
	return m
}

func (w *Workspace) addFileToModel(m *workspaceModel, path string, text []byte) {
	file, _ := hclsyntax.ParseConfig(text, path, hcl.InitialPos)
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return
	}
	for _, block := range body.Blocks {
		switch block.Type {
		case "node":
			if len(block.Labels) != 1 {
				continue
			}
			if source := literalAttribute(block, "source"); source != "" {
				m.nodes[block.Labels[0]] = inspectPorts(filepath.Dir(path), source)
			} else {
				m.nodes[block.Labels[0]] = ports{}
			}
		case "use":
			if len(block.Labels) != 1 {
				continue
			}
			as, source := literalAttribute(block, "as"), literalAttribute(block, "source")
			if as != "" && source != "" {
				m.uses[as] = inspectGroupPorts(filepath.Dir(path), source, block.Labels[0])
			}
		case "runtime":
			if len(block.Labels) == 1 {
				m.runtimes = append(m.runtimes, block.Labels[0])
			}
		}
	}
}

func inspectPorts(base, source string) ports {
	if blueprint.IsRemote(source) {
		return ports{}
	}
	schema, err := module.Inspect(filepath.Join(base, source))
	if err != nil {
		return ports{}
	}
	p := ports{inputsMeta: map[string]portMeta{}, outputsMeta: map[string]portMeta{}}
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

func inspectGroupPorts(base, source, groupName string) ports {
	if blueprint.IsRemote(source) {
		return ports{}
	}
	dir := filepath.Join(base, source)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ports{}
	}
	p := ports{inputsMeta: map[string]portMeta{}, outputsMeta: map[string]portMeta{}}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".hcl" {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		file, _ := hclsyntax.ParseConfig(contents, entry.Name(), hcl.InitialPos)
		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, block := range body.Blocks {
			if block.Type != "group" || len(block.Labels) != 1 || block.Labels[0] != groupName {
				continue
			}
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
	if diags.HasErrors() || !value.IsKnown() || value.Type() != ctyString {
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
		{name: "contracts", insert: "contracts {\n  mode = \"\"\n}", detail: "Blueprint block", documentation: "Contract strictness: legacy, warn (default), or enforce."},
	},
	"contracts": {
		{name: "mode", insert: "mode = \"warn\"", detail: "legacy | warn | enforce", documentation: "Contract strictness; enforce turns C001-C006 into errors."},
	},
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

// contractsCompletionSchemas mirrors docs/contracts.md's grammar for files named contracts.hcl, keyed the same way completionSchemas is (block-path suffix). The two grammars share no vocabulary: this file's blocks are never blueprint blocks and vice versa.
var contractsCompletionSchemas = map[string][]attributeSpec{
	"": {
		{name: "producer", insert: "producer \"./modules/name\" {\n  output \"port\" {\n    type = \"\"\n  }\n}", detail: "Contract block", documentation: "Guarantees about every output of one module source directory."},
		{name: "consumer", insert: "consumer \"./modules/name\" {\n  input \"port\" {\n    type = \"\"\n  }\n}", detail: "Contract block", documentation: "Requirements for one input of one module source directory."},
	},
	"producer": {
		{name: "output", insert: "output \"name\" {\n  type = \"\"\n}", detail: "Producer port", documentation: "One output this source guarantees."},
	},
	"consumer": {
		{name: "input", insert: "input \"name\" {\n  type = \"\"\n}", detail: "Consumer port", documentation: "One input this source requires."},
	},
	"producer.output": {
		{name: "type", insert: "type = \"string\"", detail: "Terraform type constraint", documentation: "Type this output guarantees."},
		{name: "nullable", insert: "nullable = false", detail: "bool", documentation: "Promise the value is never null."},
		{name: "sensitive", insert: "sensitive = true", detail: "bool", documentation: "Mark the value sensitive; consumers must opt in."},
		{name: "stability", insert: "stability = \"stable\"", detail: "stable | volatile", documentation: "Whether the value's meaning is stable across applies."},
		{name: "assert", insert: "assert {\n  nonempty = true\n}", detail: "Predicate block", documentation: "Value predicates."},
	},
	"consumer.input": {
		{name: "type", insert: "type = \"string\"", detail: "Terraform type constraint", documentation: "Type this input requires."},
		{name: "nullable", insert: "nullable = false", detail: "bool", documentation: "Require a non-null value."},
		{name: "sensitive", insert: "sensitive = true", detail: "bool", documentation: "Accept sensitive values from upstream."},
		{name: "stability", insert: "stability = \"stable\"", detail: "stable | volatile", documentation: "Whether the expected meaning is stable across applies."},
		{name: "assert", insert: "assert {\n  pattern = \"\"\n}", detail: "Predicate block", documentation: "Value predicates."},
	},
	"producer.output.assert": assertAttrs,
	"consumer.input.assert":  assertAttrs,
}

// assertAttrs is the closed predicate vocabulary (docs/contracts.md): additions only, never syntax changes, because digests must stay comparable.
var assertAttrs = []attributeSpec{
	{name: "nonempty", insert: "nonempty = true", detail: "bool", documentation: "Value must not be empty."},
	{name: "pattern", insert: "pattern = \"\"", detail: "regex", documentation: "Value must match this regular expression."},
	{name: "min_length", insert: "min_length = 1", detail: "number", documentation: "Minimum length for strings and lists."},
	{name: "one_of", insert: "one_of = [\"\"]", detail: "list(string)", documentation: "Value must be one of these."},
}

// The attribute names contractsDiagnostics accepts, mirroring blueprint.portContractSchema and blueprint.assertSchema.
var contractsPortAttrs = []string{"type", "nullable", "sensitive", "stability"}
var contractsAssertAttrs = []string{"nonempty", "pattern", "min_length", "one_of"}

// contextCompletions suggests what may be written inside the block containing the cursor. path is that block's chain of enclosing Blueprint blocks (see blockPathAt), matched against completionSchemas by longest suffix, so a nested block picks its own schema ("edge.input") while one that only ever means one thing keeps a single entry wherever it appears ("node", inside a group or not).
func contextCompletions(path []string, prefix string, start, end int) []Completion {
	return contextCompletionsIn(completionSchemas, path, prefix, start, end)
}

// contextCompletionsIn is contextCompletions parameterized by schema map: the blueprint grammar and the contracts.hcl grammar share the suffix-matching machinery but share no vocabulary.
func contextCompletionsIn(schemas map[string][]attributeSpec, path []string, prefix string, start, end int) []Completion {
	fields := schemaForPathIn(schemas, path)
	return stringCompletions(specNames(fields), prefix, func(name string) Completion {
		for _, field := range fields {
			if field.name == name {
				return Completion{Label: field.name, Insert: field.insert, Detail: field.detail, Documentation: field.documentation, Start: start, End: end}
			}
		}
		return Completion{}
	})
}

func schemaForPathIn(schemas map[string][]attributeSpec, path []string) []attributeSpec {
	if len(path) == 0 {
		return schemas[""]
	}
	for i := range path {
		if fields, ok := schemas[strings.Join(path[i:], ".")]; ok {
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

var attrLine = regexp.MustCompile(`(?m)^\s*([[:alnum:]_-]+)\s*=`)

func directionAt(text []byte, offset int) string {
	line := strings.LastIndex(string(text[:offset]), "\n") + 1
	match := attrLine.FindSubmatch(text[line:offset])
	if len(match) == 2 {
		return string(match[1])
	}
	return ""
}
func traversalStart(text []byte, offset int) int {
	start := offset
	for start > 0 && (text[start-1] == '.' || text[start-1] == '_' || text[start-1] == '-' || (text[start-1] >= 'a' && text[start-1] <= 'z') || (text[start-1] >= 'A' && text[start-1] <= 'Z') || (text[start-1] >= '0' && text[start-1] <= '9')) {
		start--
	}
	return start
}

var objectAttribute = regexp.MustCompile(`(?s)([[:alnum:]_-]+)\s*=\s*\{[^{}]*$`)

// objectAttributeAt returns the attribute owning the current simple object
// expression. It prevents completions for the outer node block from leaking
// into backend_config and env maps, while vars receives its specialised
// Terraform-input completion.
func objectAttributeAt(text []byte, offset int) string {
	match := objectAttribute.FindStringSubmatch(string(text[:offset]))
	if len(match) != 2 {
		return ""
	}
	return match[1]
}
func nodeAt(text []byte, offset int) string {
	before := string(text[:offset])
	matches := regexp.MustCompile(`(?s)node\s+"([^"]+)"\s*\{`).FindAllStringSubmatch(before, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1][1]
}

var useAsLiteral = regexp.MustCompile(`as\s*=\s*"([^"]+)"`)

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

func useAsAt(text []byte, offset int) string {
	start, end, ok := enclosingNamedBlock(text, offset, "use")
	if !ok {
		return ""
	}
	match := useAsLiteral.FindSubmatch(text[start:end])
	if len(match) != 2 {
		return ""
	}
	return string(match[1])
}

func enclosingNamedBlock(text []byte, offset int, name string) (start, end int, ok bool) {
	var brace int
	found := false
	for _, b := range openBlocksAt(text, offset) {
		if b.name == name {
			brace = b.at
			found = true
		}
	}
	if !found {
		return 0, 0, false
	}
	depth := 0
	for i := brace; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return brace, i, true
			}
		}
	}
	return brace, len(text), true
}

var blockHeader = regexp.MustCompile(`(?s)(node|edge|runtime|group|use|vendor|tfvars|lock|s3|export|input|output|producer|consumer|assert)\s*(?:"[^\"]*")?\s*$`)

// openBlock is one Blueprint block still open at some offset: its keyword (empty
// for a brace that opens an object rather than a block, e.g. vars or env) and the
// byte offset of its opening brace.
type openBlock struct {
	name string
	at   int
}

// openBlocksAt returns the chain of braces open at offset, outermost first.
// Object braces are tracked too, with an empty name, so a closing brace for vars
// or env cannot end the outer block.
func openBlocksAt(text []byte, offset int) []openBlock {
	stack := []openBlock{}
	for i := 0; i < offset; i++ {
		switch text[i] {
		case '{':
			name := ""
			if match := blockHeader.FindSubmatch(text[:i]); len(match) == 2 {
				name = string(match[1])
			}
			stack = append(stack, openBlock{name: name, at: i})
		case '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return stack
}

// blockPathAt returns the Blueprint blocks containing offset, outermost first
// (e.g. ["edge", "input"]), skipping object braces. Empty at the top level.
func blockPathAt(text []byte, offset int) []string {
	path := []string{}
	for _, b := range openBlocksAt(text, offset) {
		if b.name != "" {
			path = append(path, b.name)
		}
	}
	return path
}

func isEdgeInput(path []string) bool {
	return len(path) >= 2 && path[len(path)-1] == "input" && path[len(path)-2] == "edge"
}

// enclosingEdge returns the text of the innermost `edge` block containing
// offset, from its opening brace to its matching close, or to the end of the
// document: a file being typed into is routinely still unbalanced.
func enclosingEdge(text []byte, offset int) ([]byte, bool) {
	open := -1
	for _, b := range openBlocksAt(text, offset) {
		if b.name == "edge" {
			open = b.at
		}
	}
	if open < 0 {
		return nil, false
	}
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[open : i+1], true
			}
		}
	}
	return text[open:], true
}

var edgeEndpoint = regexp.MustCompile(`(?m)^\s*(from|to)\s*=\s*(node|use)\.([[:alnum:]_-]+)\s*$`)

// edgeEndpointPorts returns the ports of the node or group instance the edge
// containing offset names on the given side, provided that side is a bare
// reference: only such an edge can carry nested input blocks, and the whole
// block is searched (not just the text before the cursor) so the input blocks
// may be written above from and to.
func edgeEndpointPorts(m workspaceModel, text []byte, offset int, side string) (ports, bool) {
	block, ok := enclosingEdge(text, offset)
	if !ok {
		return ports{}, false
	}
	for _, match := range edgeEndpoint.FindAllSubmatch(block, -1) {
		if string(match[1]) != side {
			continue
		}
		return lookupEntity(m, string(match[2]), string(match[3]))
	}
	return ports{}, false
}

func lookupEntity(m workspaceModel, keyword, name string) (ports, bool) {
	if keyword == "use" {
		p, ok := m.uses[name]
		return p, ok
	}
	p, ok := m.nodes[name]
	return p, ok
}

var edgeInputLabel = regexp.MustCompile(`(?s)input\s+"([^"\n]*)$`)

// edgeInputLabelAt reports whether offset sits inside the label of an edge's
// nested input block (`input "vp|`), returning where that label starts. The
// label names an input of the edge's to node, so it completes like a port.
func edgeInputLabelAt(text []byte, offset int, path []string) (int, bool) {
	if len(path) == 0 || path[len(path)-1] != "edge" {
		return 0, false
	}
	match := edgeInputLabel.FindSubmatchIndex(text[:offset])
	if match == nil {
		return 0, false
	}
	return match[2], true
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

// Definition resolves the node or runtime segment under offset. It searches
// every .hcl file directly in the same blueprint directory, matching the
// parser's multi-file blueprint layout.
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

// Diagnose checks references that can be validated without evaluating HCL.
// It deliberately accepts incomplete documents so errors update while typing.
func (w *Workspace) Diagnose(_ context.Context, path string) []Diagnostic {
	path = absolute(path)
	text := w.document(path)
	if isContractsFile(path) {
		return contractsDiagnostics(path, text)
	}
	model := w.model(path, text)
	diagnostics := []Diagnostic{}
	for _, match := range nodeReference.FindAllSubmatchIndex(text, -1) {
		nameStart, nameEnd := match[2], match[3]
		kindStart, kindEnd := match[4], match[5]
		portStart, portEnd := match[6], match[7]
		name := string(text[nameStart:nameEnd])
		ports, ok := model.nodes[name]
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Start: nameStart, End: nameEnd, Message: "Unknown node " + name})
			continue
		}
		if kindStart < 0 {
			continue // Bare nodes are valid ordering-only edge endpoints.
		}
		kind := string(text[kindStart:kindEnd])
		direction := directionAt(text, nameStart)
		if (direction == "from" && kind != "output") || (direction == "to" && kind != "input") {
			expected := "output"
			if direction == "to" {
				expected = "input"
			}
			diagnostics = append(diagnostics, Diagnostic{Start: kindStart, End: kindEnd, Message: direction + " must reference node " + expected})
		}
		if portStart < 0 {
			continue
		}
		port := string(text[portStart:portEnd])
		available := ports.inputs
		if kind == "output" {
			available = ports.outputs
		}
		if !containsString(available, port) {
			diagnostics = append(diagnostics, Diagnostic{Start: portStart, End: portEnd, Message: "Unknown " + kind + " " + port + availableHint(available)})
		}
	}
	for _, match := range objectKey.FindAllSubmatchIndex(text, -1) {
		start, end := match[2], match[3]
		if objectAttributeAt(text, start) != "vars" {
			continue
		}
		ports, ok := varsPortsAt(model, text, start)
		if !ok || containsString(ports.inputs, string(text[start:end])) {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{Start: start, End: end, Message: "Unknown input " + string(text[start:end]) + availableHint(ports.inputs)})
	}
	return append(diagnostics, edgeInputDiagnostics(model, path, text)...)
}

// contractsDiagnostics gives contracts.hcl parse-level parity with blueprint.ParseContracts: unknown blocks and attributes, non-relative scopes, duplicate (scope, role, port) declarations, and the stability enum — everything checkable from the file alone, so errors update while typing.
func contractsDiagnostics(path string, text []byte) []Diagnostic {
	file, _ := hclsyntax.ParseConfig(text, path, hcl.InitialPos)
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}

	diagnostics := []Diagnostic{}
	seen := map[[3]string]bool{}
	for _, block := range body.Blocks {
		if block.Type != "producer" && block.Type != "consumer" {
			diagnostics = append(diagnostics, Diagnostic{Start: block.TypeRange.Start.Byte, End: block.TypeRange.End.Byte, Message: "Unsupported block " + block.Type + "; contracts.hcl allows only producer and consumer blocks"})
			continue
		}
		scope := ""
		if len(block.Labels) == 1 && len(block.LabelRanges) == 1 {
			scope = block.Labels[0]
			if !strings.HasPrefix(scope, "./") && !strings.HasPrefix(scope, "../") {
				start, end := unquotedRange(text, block.LabelRanges[0])
				diagnostics = append(diagnostics, Diagnostic{Start: start, End: end, Message: "contract scope must be a relative path like \"./modules/vpc\", got \"" + scope + "\""})
			}
		}
		portKind := "output"
		if block.Type == "consumer" {
			portKind = "input"
		}
		for _, port := range block.Body.Blocks {
			if port.Type != portKind {
				diagnostics = append(diagnostics, Diagnostic{Start: port.TypeRange.Start.Byte, End: port.TypeRange.End.Byte, Message: "Unsupported block " + port.Type + "; a " + block.Type + " block contains only " + portKind + " port blocks"})
				continue
			}
			name := ""
			if len(port.Labels) == 1 && len(port.LabelRanges) == 1 {
				name = port.Labels[0]
				key := [3]string{scope, block.Type, name}
				if seen[key] {
					start, end := unquotedRange(text, port.LabelRanges[0])
					diagnostics = append(diagnostics, Diagnostic{Start: start, End: end, Message: "contract " + block.Type + " " + portKind + " \"" + name + "\" is declared more than once; remove one"})
				}
				seen[key] = true
			}
			for _, attr := range sortedAttributes(port.Body.Attributes) {
				diagnosePortAttribute(&diagnostics, attr)
			}
			for _, assert := range port.Body.Blocks {
				if assert.Type != "assert" {
					diagnostics = append(diagnostics, Diagnostic{Start: assert.TypeRange.Start.Byte, End: assert.TypeRange.End.Byte, Message: "Unsupported block " + assert.Type + "; a port block contains only an assert block"})
					continue
				}
				for _, attr := range sortedAttributes(assert.Body.Attributes) {
					if !containsString(contractsAssertAttrs, attr.Name) {
						diagnostics = append(diagnostics, Diagnostic{Start: attr.NameRange.Start.Byte, End: attr.NameRange.End.Byte, Message: "Unknown attribute " + attr.Name + "; assert accepts " + strings.Join(contractsAssertAttrs, ", ")})
					}
				}
			}
		}
	}
	return diagnostics
}

// diagnosePortAttribute flags unknown port attribute names and the stability enum, mirroring blueprint.parsePortContract.
func diagnosePortAttribute(diagnostics *[]Diagnostic, attr *hclsyntax.Attribute) {
	if !containsString(contractsPortAttrs, attr.Name) {
		*diagnostics = append(*diagnostics, Diagnostic{Start: attr.NameRange.Start.Byte, End: attr.NameRange.End.Byte, Message: "Unknown attribute " + attr.Name + "; a port accepts " + strings.Join(contractsPortAttrs, ", ") + " and an assert block"})
		return
	}
	if attr.Name != "stability" {
		return
	}
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() || val.Type() != ctyString {
		return
	}
	if got := val.AsString(); got != "stable" && got != "volatile" {
		rng := attr.Expr.Range()
		*diagnostics = append(*diagnostics, Diagnostic{Start: rng.Start.Byte, End: rng.End.Byte, Message: "stability must be \"stable\" or \"volatile\", got \"" + got + "\""})
	}
}

// sortedAttributes iterates a body's attributes by name so diagnostics come in a stable order (Go map order is randomized per run).
func sortedAttributes(attrs hclsyntax.Attributes) []*hclsyntax.Attribute {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	sorted := make([]*hclsyntax.Attribute, 0, len(names))
	for _, name := range names {
		sorted = append(sorted, attrs[name])
	}
	return sorted
}

// edgeInputDiagnostics checks an edge's nested input blocks, which the reference
// regexes above cannot see: neither the block label nor its relative
// `from = output.<attr>` names the node it belongs to, so both are only
// meaningful against the endpoints of the enclosing edge. Only top-level edges
// are checked, since an edge inside a group definition references that group's
// own internal nodes, which the workspace model does not track.
func edgeInputDiagnostics(m workspaceModel, path string, text []byte) []Diagnostic {
	file, _ := hclsyntax.ParseConfig(text, path, hcl.InitialPos)
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}

	diagnostics := []Diagnostic{}
	for _, block := range body.Blocks {
		if block.Type != "edge" {
			continue
		}
		source, hasSource := blockEndpointPorts(m, block, "from")
		target, hasTarget := blockEndpointPorts(m, block, "to")

		for _, input := range block.Body.Blocks {
			if input.Type != "input" || len(input.Labels) != 1 || len(input.LabelRanges) != 1 {
				continue
			}
			if hasTarget && !containsString(target.inputs, input.Labels[0]) {
				start, end := unquotedRange(text, input.LabelRanges[0])
				diagnostics = append(diagnostics, Diagnostic{Start: start, End: end, Message: "Unknown input " + input.Labels[0] + availableHint(target.inputs)})
			}

			attr := input.Body.Attributes["from"]
			if attr == nil || !hasSource {
				continue
			}
			name, ok := relativeOutputName(attr.Expr)
			if !ok {
				continue // Not a relative output reference at all; the parser reports that with its own message.
			}
			if !containsString(source.outputs, name) {
				rng := attr.Expr.Range()
				diagnostics = append(diagnostics, Diagnostic{Start: rng.Start.Byte, End: rng.End.Byte, Message: "Unknown output " + name + availableHint(source.outputs)})
			}
		}
	}
	return diagnostics
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

var nodeReference = regexp.MustCompile(`\bnode\.([[:alnum:]_-]+)(?:\.(input|output)(?:\.([[:alnum:]_-]+))?)?`)
var objectKey = regexp.MustCompile(`(?m)^\s*([[:alnum:]_-]+)\s*=`)

func availableHint(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return "; available: " + strings.Join(values, ", ")
}

var reference = regexp.MustCompile(`\b(node|runtime)\.([[:alnum:]_-]+)\b`)

func referenceAt(text []byte, offset int) (string, string, bool) {
	for _, match := range reference.FindAllSubmatchIndex(text, -1) {
		start, end := match[4], match[5]
		if offset >= start && offset <= end {
			return string(text[match[2]:match[3]]), string(text[start:end]), true
		}
	}
	return "", "", false
}

func (w *Workspace) blueprintFiles(path string) []string {
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{path}
	}
	files := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".hcl" {
			if entry.Name() == "contracts.hcl" {
				continue // reserved sibling (see blueprint.ParseDir): contracts content is not blueprint content
			}
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	if !containsString(files, path) {
		files = append(files, path)
	}
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
