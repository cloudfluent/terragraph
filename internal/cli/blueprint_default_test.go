package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraph_DefaultBlueprintMergesDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixtureFile(t, filepath.Join(dir, "nodes.hcl"), "node \"a\" { source = \"./a\" }\nnode \"b\" { source = \"./b\" }\n")
	writeFixtureFile(t, filepath.Join(dir, "edges.hcl"), "edge {\n from = node.a.output.id\n to = node.b.input.id\n}\n")
	writeFixtureFile(t, filepath.Join(dir, "a", "main.tf"), `output "id" { value = "a" }`)
	writeFixtureFile(t, filepath.Join(dir, "b", "main.tf"), `variable "id" { type = string }`)
	writeFixtureFile(t, filepath.Join(dir, ".terraform.lock.hcl"), `provider "registry.terraform.io/hashicorp/null" { version = "3.2.4" }`)
	writeFixtureFile(t, filepath.Join(dir, "unreferenced", "invalid.hcl"), "not valid hcl {{{")
	stdout, stderr, err := runRootCmd(t, "graph")
	if err != nil || stderr != "" || stdout != "level 1: a\nlevel 2: b\n" {
		t.Fatalf("got = %q, %q, %v, want two execution levels", stdout, stderr, err)
	}
	explicit, diagnostics, err := runRootCmd(t, "graph", "--blueprint", ".")
	if err != nil || diagnostics != stderr || explicit != stdout {
		t.Fatalf("got = %q, %q, %v, want same explicit-directory result", explicit, diagnostics, err)
	}
}

func TestGraph_ExplicitBlueprintKeepsSingleFileLoading(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), `node "a" { source = "./a" }`)
	writeFixtureFile(t, filepath.Join(dir, "a", "main.tf"), `output "id" { value = "a" }`)
	writeFixtureFile(t, filepath.Join(dir, "backend.hcl"), `bucket = "state"`)
	stdout, stderr, err := runRootCmd(t, "graph", "--blueprint", "blueprint.hcl")
	if err != nil || stderr != "" || stdout != "level 1: a\n" {
		t.Fatalf("got = %q, %q, %v, want only node a", stdout, stderr, err)
	}
	if _, _, err := runRootCmd(t, "graph"); err == nil || !strings.Contains(err.Error(), "Unsupported argument") {
		t.Fatalf("got = %v, want sibling HCL error", err)
	}
}

func TestRoot_DefaultBlueprintRejectsMissingConfiguration(t *testing.T) {
	for _, command := range []string{"validate", "graph", "plan", "apply", "destroy", "output", "status", "vendor", "force-unlock"} {
		t.Run(command, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			stdout, _, err := runRootCmd(t, command)
			if err == nil || stdout != "" {
				t.Fatalf("got = %q, %v, want missing configuration failure", stdout, err)
			}
			if _, err := os.Stat(filepath.Join(dir, ".terragraph")); !os.IsNotExist(err) {
				t.Fatalf("got = %v, want failure before creating managed files", err)
			}
		})
	}
}
