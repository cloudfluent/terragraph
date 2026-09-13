package engine

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// TestSavedGraphBinding_IgnoresProvenance pins the saved-plan binding to topology and settings: shifting declaration line numbers (here via a comment-only edit) must not change the digest, or every re-checked-out copy of a blueprint would fail `apply --plan` with saved_plan_incompatible.
func TestSavedGraphBinding_IgnoresProvenance(t *testing.T) {
	build := func(t *testing.T, extraComment bool) string {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "modules", "app"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "modules", "app", "main.tf"), []byte("variable \"name\" {\n  type = string\n}\noutput \"id\" {\n  value = var.name\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		shift := ""
		if extraComment {
			shift = "// a comment added after the plan was saved; nothing about the graph changed\n"
		}
		if err := os.WriteFile(filepath.Join(root, "blueprint.hcl"), []byte(shift+"node \"app\" {\n  source = \"./modules/app\"\n  vars = {\n    name = \"one\"\n  }\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		e, err := Load(filepath.Join(root, "blueprint.hcl"), exec.Terraform, io.Discard, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		names := make([]ExecutionNode, 0, len(e.Graph.Nodes))
		for name := range e.Graph.Nodes {
			names = append(names, ExecutionNode{Name: name})
		}
		binding, err := e.savedGraphBinding(ExecutionRecord{Nodes: names})
		if err != nil {
			t.Fatalf("savedGraphBinding: %v", err)
		}
		return binding
	}
	before := build(t, false)
	after := build(t, true)
	if before == "" || before != after {
		t.Fatalf("saved-graph binding changed with a comment-only edit: before = %.16s…, after = %.16s…", before, after)
	}
}
