package language

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComplete_ContractNativeType(t *testing.T) {
	w := NewWorkspace(t.TempDir())
	path := filepath.Join(w.root, "contracts.hcl")
	text := `producer "./m" {
 output "value" {
  type = li
 }
}`
	w.SetDocument(path, []byte(text))
	offset := strings.Index(text, "li\n") + 2
	items := w.Complete(context.Background(), path, offset)
	if len(items) != 1 || items[0].Insert != "list(string)" {
		t.Fatalf("got = %#v, want list(string)", items)
	}
}

func TestDiagnose_ContractTypeAtOriginalRange(t *testing.T) {
	w := NewWorkspace(t.TempDir())
	path := filepath.Join(w.root, "contracts.hcl")
	text := `consumer "./m" {
 input "value" {
  type = object({ id = optional(string, "default") })
 }
}`
	w.SetDocument(path, []byte(text))
	diags := w.Diagnose(context.Background(), path)
	if len(diags) != 1 || diags[0].Start != strings.Index(text, "object(") {
		t.Fatalf("got = %#v, want diagnostic at type expression", diags)
	}
	text = strings.ReplaceAll(text, `optional(string, "default")`, `optional(string)`)
	w.SetDocument(path, []byte(text))
	if got := w.Diagnose(context.Background(), path); len(got) != 0 {
		t.Fatalf("got = %#v, want no diagnostics", got)
	}
}

func TestHover_ContractInheritsVariableType(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "m"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "m", "main.tf"), []byte(`variable "value" { type = list(string) }`), 0600); err != nil {
		t.Fatal(err)
	}
	text := `consumer "./m" {
 input "value" {
  nullable = false
 }
}`
	w := NewWorkspace(root)
	path := filepath.Join(root, "contracts.hcl")
	w.SetDocument(path, []byte(text))
	if got := w.Hover(context.Background(), path, strings.Index(text, "nullable")); !strings.Contains(got, "list(string)") {
		t.Fatalf("got = %q, want inherited module type", got)
	}
}
