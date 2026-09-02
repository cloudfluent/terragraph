package language

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceCompletePortsAndVarsInIncompleteDocument(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "stacks", "vpc", "main.tf"), `output "vpc_id" { value = "x" }`)
	writeFile(t, filepath.Join(dir, "stacks", "eks", "main.tf"), `variable "vpc_id" { type = string }
variable "cluster_name" { type = string }`)
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

	for _, tc := range []struct{ marker, want string }{
		{"cl__CURSOR__", "cluster_name"},
		{"output.__CURSOR__", "vpc_id"},
		{"input.__CURSOR__", "vpc_id"},
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
		})
	}
}

func TestWorkspaceCompleteGroupExports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "groups", "network", "group.hcl"), `group "network" {
  export {
    input = { cidr = node.vpc.input.cidr }
    output = { vpc_id = node.vpc.output.vpc_id }
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
