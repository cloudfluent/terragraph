package language

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func TestWorkspaceCompletePortsAndVarsInIncompleteDocument(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "stacks", "vpc", "main.tf"), `output "vpc_id" {
  value = "x"
  description = "VPC identifier"
  sensitive = true
}`)
	writeFile(t, filepath.Join(dir, "stacks", "eks", "main.tf"), `variable "vpc_id" { type = string }
variable "cluster_name" {
  type = string
  description = "Cluster name"
  sensitive = true
}`)
	path := filepath.Join(dir, "blueprint.hcl")
	text := `node "vpc" { source = "./stacks/vpc" }
node "eks" {
  source = "./stacks/eks"
  vars = {
    cl__CURSOR__
  }
}
edge {
  from = node.vpc.output.__CURSOR__
  to   = node.eks.input.__CURSOR__
}`

	for _, tc := range []struct{ marker, want, detail string }{
		{"cl__CURSOR__", "cluster_name", "string (required, sensitive) — Cluster name"},
		{"output.__CURSOR__", "vpc_id", "(sensitive) — VPC identifier"},
		{"input.__CURSOR__", "vpc_id", "string (required)"},
	} {
		t.Run(tc.want+tc.marker, func(t *testing.T) {
			clean, offset := cursor(text, tc.marker)
			if err := os.WriteFile(path, []byte(clean), 0o644); err != nil {
				t.Fatal(err)
			}
			ws := NewWorkspace(dir)
			items := ws.Complete(context.Background(), path, offset)
			if !contains(items, tc.want) {
				t.Fatalf("completion %q missing from %#v", tc.want, items)
			}
			if got := detailOf(items, tc.want); got != tc.detail {
				t.Fatalf("completion %q detail = %q, want %q", tc.want, got, tc.detail)
			}
			if tc.want == "cluster_name" && documentationOf(items, tc.want) != "Cluster name" {
				t.Fatalf("completion documentation missing from %#v", items)
			}
		})
	}
}

func TestWorkspaceCompleteGroupExports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "groups", "network", "group.hcl"), `group "network" {
  export {
    input "cidr" { to = node.vpc.input.cidr }
    output "vpc_id" { from = node.vpc.output.vpc_id }
  }
}`)
	path := filepath.Join(dir, "blueprint.hcl")
	text, offset := cursor(`use "network" {
  as = "network"
  source = "./groups/network"
}
	edge {
  from = use.network.output.__CURSOR__
  to = use.network.input.cidr
}`, "__CURSOR__")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if !contains(NewWorkspace(dir).Complete(context.Background(), path, offset), "vpc_id") {
		t.Fatal("expected exported group output")
	}
}

func TestWorkspaceCompletesBlueprintSyntaxByContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	for _, tc := range []struct {
		name, text, want, detail string
	}{
		{"top level", "__CURSOR__", "node", "Blueprint block"},
		{"node attribute", "node \"vpc\" {\n  __CURSOR__\n}", "vars", "object"},
		{"edge attribute", "edge {\n  __CURSOR__\n}", "from", "required output reference"},
		{"runtime attribute", "runtime \"tofu\" {\n  __CURSOR__\n}", "binary", "required string"},
		{"use attribute", "use \"network\" {\n  __CURSOR__\n}", "env", "map(string)"},
		{"use vars attribute", "use \"network\" {\n  __CURSOR__\n}", "vars", "object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, offset := cursor(tc.text, "__CURSOR__")
			ws := NewWorkspace(dir)
			ws.SetDocument(path, []byte(text))
			items := ws.Complete(context.Background(), path, offset)
			if !contains(items, tc.want) {
				t.Fatalf("completion %q missing from %#v", tc.want, items)
			}
			if got := detailOf(items, tc.want); got != tc.detail {
				t.Fatalf("completion %q detail = %q, want %q", tc.want, got, tc.detail)
			}
		})
	}
}

func TestWorkspaceCompletesRuntimeNamesAndFindsDefinitionsAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "runtimes.hcl"), `runtime "tofu" { binary = "tofu" }`)
	writeFile(t, filepath.Join(dir, "nodes.hcl"), `node "vpc" { source = "./stacks/vpc" }`)
	path := filepath.Join(dir, "edges.hcl")
	text, offset := cursor(`node "app" {
  runtime = runtime.__CURSOR__
}

edge { from = node.vpc.output.id to = node.app.input.id }`, "__CURSOR__")
	writeFile(t, path, text)
	ws := NewWorkspace(dir)
	if !contains(ws.Complete(context.Background(), path, offset), "tofu") {
		t.Fatal("expected declared runtime completion")
	}

	definitionOffset := strings.Index(text, "vpc.output") + 1
	target, ok := ws.Definition(context.Background(), path, definitionOffset)
	if !ok {
		t.Fatal("expected node definition")
	}
	if target.Path != filepath.Join(dir, "nodes.hcl") {
		t.Fatalf("definition path = %q", target.Path)
	}
}

func TestWorkspaceDoesNotOfferNodeAttributesInsideBackendConfigObject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	text, offset := cursor(`node "vpc" {
  source = "./stacks/vpc"
  backend_config = {
    __CURSOR__
  }
}`, "__CURSOR__")
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(text))
	if items := ws.Complete(context.Background(), path, offset); len(items) != 0 {
		t.Fatalf("backend_config completion = %#v, want no node attribute suggestions", items)
	}
}

func TestWorkspaceCompletesUseVarsAgainstExportInputs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "groups", "network", "group.hcl"), `group "network" {
  export {
    input "cidr" { to = node.vpc.input.cidr }
    input "vpc_id" { to = node.vpc.input.vpc_id }
  }
}`)
	writeFile(t, filepath.Join(dir, "stacks", "app", "main.tf"), `variable "cluster_name" { type = string }`)
	path := filepath.Join(dir, "blueprint.hcl")
	text, offset := cursor(`node "app" { source = "./stacks/app" }
use "network" {
  as     = "network"
  source = "./groups/network"
  vars = {
    __CURSOR__
  }
}`, "__CURSOR__")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	items := NewWorkspace(dir).Complete(context.Background(), path, offset)
	if !contains(items, "cidr") || !contains(items, "vpc_id") {
		t.Fatalf("expected export input completions, got %#v", items)
	}
	if contains(items, "cluster_name") {
		t.Fatalf("use.vars must not suggest a sibling node's module variables, got %#v", items)
	}
}

func TestWorkspaceDiagnosesInvalidUseVars(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "groups", "network", "group.hcl"), `group "network" {
  export {
    input "cidr" { to = node.vpc.input.cidr }
  }
}`)
	path := filepath.Join(dir, "blueprint.hcl")
	text := `use "network" {
  as     = "network"
  source = "./groups/network"
  vars = {
    typo = "x"
  }
}`
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(text))
	got := ws.Diagnose(context.Background(), path)
	found := false
	for _, diagnostic := range got {
		if strings.Contains(diagnostic.Message, "Unknown input typo") {
			found = true
			if !strings.Contains(diagnostic.Message, "cidr") {
				t.Fatalf("expected available export inputs in diagnostic, got %#v", diagnostic)
			}
			break
		}
	}
	if !found {
		t.Fatalf("diagnostic %q missing from %#v", "Unknown input typo", got)
	}
}

func TestWorkspaceDiagnosesInvalidNodePortsAndVars(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "stacks", "vpc", "main.tf"), `output "vpc_id" { value = "x" }`)
	writeFile(t, filepath.Join(dir, "stacks", "app", "main.tf"), `variable "vpc_id" { type = string }`)
	path := filepath.Join(dir, "blueprint.hcl")
	text := `node "vpc" { source = "./stacks/vpc" }
node "app" {
  source = "./stacks/app"
  vars = {
    typo = "x"
  }
}
edge {
  from = node.typo.output.vpc_id
  to   = node.app.output.missing
}`
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(text))
	got := ws.Diagnose(context.Background(), path)
	for _, want := range []string{"Unknown input typo", "Unknown node typo", "to must reference node input", "Unknown output missing"} {
		found := false
		for _, diagnostic := range got {
			if strings.Contains(diagnostic.Message, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("diagnostic %q missing from %#v", want, got)
		}
	}
}

// An edge's nested input blocks name ports without repeating the node they belong to, so every suggestion here has to come from the enclosing edge's own endpoints.
func TestWorkspaceCompletesEdgeInputBlockAgainstEdgeEndpoints(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "stacks", "vpc", "main.tf"), `output "vpc_id" { value = "x" }
output "subnet_ids" { value = [] }`)
	writeFile(t, filepath.Join(dir, "stacks", "eks", "main.tf"), `variable "vpc_id" { type = string }
variable "subnet_ids" { type = list(string) }`)
	path := filepath.Join(dir, "blueprint.hcl")
	const endpoints = `node "vpc" { source = "./stacks/vpc" }
node "eks" { source = "./stacks/eks" }
edge {
  from = node.vpc
  to   = node.eks
`

	for _, tc := range []struct{ name, text, want, detail string }{
		{"block label", endpoints + "  input \"__CURSOR__\"\n}", "subnet_ids", "list(string) (required)"},
		{"block attribute", endpoints + "  input \"vpc_id\" {\n    __CURSOR__\n  }\n}", "from", "required output reference"},
		{"relative output", endpoints + "  input \"vpc_id\" {\n    from = output.__CURSOR__\n  }\n}", "subnet_ids", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clean, offset := cursor(tc.text, "__CURSOR__")
			ws := NewWorkspace(dir)
			ws.SetDocument(path, []byte(clean))
			items := ws.Complete(context.Background(), path, offset)
			if !contains(items, tc.want) {
				t.Fatalf("completion %q missing from %#v", tc.want, items)
			}
			if got := detailOf(items, tc.want); got != tc.detail {
				t.Fatalf("completion %q detail = %q, want %q", tc.want, got, tc.detail)
			}
		})
	}
}

func TestWorkspaceDiagnosesEdgeInputBlockPorts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "stacks", "vpc", "main.tf"), `output "vpc_id" { value = "x" }`)
	writeFile(t, filepath.Join(dir, "stacks", "eks", "main.tf"), `variable "vpc_id" { type = string }`)
	path := filepath.Join(dir, "blueprint.hcl")
	text := `node "vpc" { source = "./stacks/vpc" }
node "eks" { source = "./stacks/eks" }
edge {
  from = node.vpc
  to   = node.eks

  input "vpc_id"  { from = output.vpc_id }
  input "missing" { from = output.absent }
}`
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(text))
	got := ws.Diagnose(context.Background(), path)
	if len(got) != 2 {
		t.Fatalf("expected exactly the two bad ports to be reported, got %#v", got)
	}
	for _, want := range []string{"Unknown input missing", "Unknown output absent"} {
		found := false
		for _, diagnostic := range got {
			if strings.Contains(diagnostic.Message, want) {
				found = true
				if string(text[diagnostic.Start:diagnostic.End]) == "" {
					t.Fatalf("diagnostic %q has an empty range", want)
				}
			}
		}
		if !found {
			t.Fatalf("diagnostic %q missing from %#v", want, got)
		}
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
func cursor(text, marker string) (string, int) {
	match := strings.Index(text, marker)
	within := strings.Index(marker, "__CURSOR__")
	if match < 0 || within < 0 {
		panic("cursor missing")
	}
	offset := match + within
	return text[:offset] + text[offset+len("__CURSOR__"):], offset
}
func contains(items []Completion, label string) bool {
	for _, item := range items {
		if item.Label == label {
			return true
		}
	}
	return false
}

func detailOf(items []Completion, label string) string {
	for _, item := range items {
		if item.Label == label {
			return item.Detail
		}
	}
	return ""
}

func documentationOf(items []Completion, label string) string {
	for _, item := range items {
		if item.Label == label {
			return item.Documentation
		}
	}
	return ""
}

// Editor completion is a second description of the blueprint language, and a second description drifts: `approve` was added to node and use blocks and went unoffered until someone noticed. This ties it back to the schema the parser actually uses, so the next attribute cannot be added in only one of the two places.
func TestCompletionOffersEveryAttributeTheParserAccepts(t *testing.T) {
	for block, attrs := range blueprint.BlockAttributes() {
		offered := map[string]bool{}
		for _, c := range completionSchemas[block] {
			offered[c.name] = true
		}
		for _, attr := range attrs {
			if !offered[attr] {
				t.Errorf("%s block accepts %q but editor completion never offers it", block, attr)
			}
		}
	}
}
