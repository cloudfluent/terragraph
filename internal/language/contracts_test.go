package language

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func wsWithContracts(t *testing.T, text string) (*Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	w := NewWorkspace(dir)
	path := filepath.Join(dir, "contracts.hcl")
	w.SetDocument(path, []byte(text))
	return w, path
}

// TestComplete_ContractsTopLevel proves a contracts.hcl file offers its own top-level blocks, not blueprint blocks.
func TestComplete_ContractsTopLevel(t *testing.T) {
	w, path := wsWithContracts(t, "")
	items := w.Complete(context.Background(), path, 0)
	for _, want := range []string{"producer", "consumer"} {
		if !contains(items, want) {
			t.Fatalf("completion %q missing from %#v", want, items)
		}
	}
	if contains(items, "node") {
		t.Fatal("blueprint block offered inside contracts.hcl")
	}
}

// TestComplete_ContractsPortAttributes proves attribute completion inside a producer's output block knows the phase-1 vocabulary.
func TestComplete_ContractsPortAttributes(t *testing.T) {
	text := "producer \"./m\" {\n  output \"id\" {\n    \n  }\n}\n"
	w, path := wsWithContracts(t, text)
	items := w.Complete(context.Background(), path, len(text)-7)
	for _, want := range []string{"type", "nullable", "sensitive", "stability", "assert"} {
		if !contains(items, want) {
			t.Fatalf("completion %q missing from %#v", want, items)
		}
	}
}

// TestDiagnose_ContractsRules proves parse-level parity with blueprint.ParseContracts: relative-scope, duplicate-port, and stability-enum violations surface as editor diagnostics.
func TestDiagnose_ContractsRules(t *testing.T) {
	w, path := wsWithContracts(t, `
producer "/abs" {
  output "id" { type = "string" }
}
producer "./m" {
  output "id" { stability = "sometimes" }
  output "id" { type = "string" }
}
`)
	var joined strings.Builder
	for _, d := range w.Diagnose(context.Background(), path) {
		joined.WriteString(d.Message + "\n")
	}
	for _, want := range []string{"relative", "stability", "more than once"} {
		if !strings.Contains(joined.String(), want) {
			t.Fatalf("diagnostics missing %q:\n%s", want, joined.String())
		}
	}
}

// TestDiagnose_BlueprintIgnoresContractsFile proves the blueprint model no longer ingests contracts.hcl as blueprint content (same reserved-filename rule as blueprint.ParseDir).
func TestDiagnose_BlueprintIgnoresContractsFile(t *testing.T) {
	root := t.TempDir()
	w := NewWorkspace(root)
	w.SetDocument(filepath.Join(root, "contracts.hcl"), []byte("producer \"./m\" {\n  output \"id\" { type = \"string\" }\n}\n"))
	w.SetDocument(filepath.Join(root, "blueprint.hcl"), []byte("node \"a\" {\n  source = \"./m\"\n}\n"))
	for _, d := range w.Diagnose(context.Background(), filepath.Join(root, "blueprint.hcl")) {
		if strings.Contains(d.Message, "producer") || strings.Contains(d.Message, "Unsupported block") {
			t.Fatalf("blueprint diagnostics leaked contracts content: %+v", d)
		}
	}
}
