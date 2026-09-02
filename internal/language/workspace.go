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
	Label  string
	Insert string
	Detail string
	Start  int
	End    int
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

	model := w.model(path, text)
	start := traversalStart(text, offset)
	fragment := string(text[start:offset])
	if isVarsObject(text, offset) && !strings.Contains(fragment, ".") {
		return propertyCompletions(model.inputsForNode(nodeAt(text, offset)), fragment, start, offset)
	}
	if strings.HasPrefix(fragment, "node") || strings.HasPrefix(fragment, "use") {
		return traversalCompletions(model, fragment, directionAt(text, offset), start, offset)
	}
	return nil
}

type workspaceModel struct {
	nodes map[string]ports
	uses  map[string]ports
}
type ports struct{ inputs, outputs []string }

func (m workspaceModel) inputsForNode(name string) []string { return m.nodes[name].inputs }

func (w *Workspace) model(path string, text []byte) workspaceModel {
	m := workspaceModel{nodes: map[string]ports{}, uses: map[string]ports{}}
	file, _ := hclsyntax.ParseConfig(text, path, hcl.InitialPos)
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return m
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
		}
	}
	return m
}

func inspectPorts(base, source string) ports {
	if blueprint.IsRemote(source) {
		return ports{}
	}
	schema, err := module.Inspect(filepath.Join(base, source))
	if err != nil {
		return ports{}
	}
	p := ports{}
	for name := range schema.Variables {
		p.inputs = append(p.inputs, name)
	}
	for name := range schema.Outputs {
		p.outputs = append(p.outputs, name)
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
	p := ports{}
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
				if attr := export.Body.Attributes["input"]; attr != nil {
					p.inputs = append(p.inputs, objectKeys(attr.Expr)...)
				}
				if attr := export.Body.Attributes["output"]; attr != nil {
					p.outputs = append(p.outputs, objectKeys(attr.Expr)...)
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

func objectKeys(expr hcl.Expression) []string {
	obj, ok := expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(obj.Items))
	for _, item := range obj.Items {
		value, diags := item.KeyExpr.Value(nil)
		if !diags.HasErrors() && value.Type() == ctyString {
			keys = append(keys, value.AsString())
		}
	}
	return keys
}

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
			return Completion{Label: name, Insert: strings.Join(parts[:3], ".") + "." + name, Detail: parts[2] + " port", Start: start, End: end}
		})
	}
	return nil
}

func propertyCompletions(names []string, prefix string, start, end int) []Completion {
	return stringCompletions(names, prefix, func(name string) Completion {
		return Completion{Label: name, Insert: name, Detail: "input variable", Start: start, End: end}
	})
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
func isVarsObject(text []byte, offset int) bool {
	before := string(text[:offset])
	marker := strings.LastIndex(before, "vars")
	if marker < 0 {
		return false
	}
	equals := strings.Index(before[marker:], "=")
	if equals < 0 {
		return false
	}
	open := strings.Index(before[marker+equals:], "{")
	if open < 0 {
		return false
	}
	return strings.LastIndex(before, "}") < marker+equals+open
}
func nodeAt(text []byte, offset int) string {
	before := string(text[:offset])
	matches := regexp.MustCompile(`(?s)node\s+"([^"]+)"\s*\{`).FindAllStringSubmatch(before, -1)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1][1]
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
func absolute(path string) string {
	result, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return result
}
