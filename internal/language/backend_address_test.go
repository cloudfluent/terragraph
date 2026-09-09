package language

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceComplete_BackendAddressFields(t *testing.T) {
	for _, body := range []string{
		"node \"app\" {\n backend_address = {\n  __CURSOR__\n }\n}",
		"use \"service\" {\n backend_address = {\n  __CURSOR__\n }\n}",
		"group \"service\" {\n node \"app\" {\n backend_address = {\n __CURSOR__\n }\n }\n}",
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "blueprint.hcl")
		text, offset := cursor(body, "__CURSOR__")
		ws := NewWorkspace(dir)
		ws.SetDocument(path, []byte(text))
		items := ws.Complete(context.Background(), path, offset)
		if len(items) != 2 || !contains(items, "s3_key_prefix") || !contains(items, "s3_key_name") {
			t.Fatalf("completion = %#v, want prefix and file name fields", items)
		}
		for _, item := range items {
			if !strings.Contains(item.Insert, " = ") || item.Documentation == "" {
				t.Fatalf("completion = %#v, want insertable assignment and documentation", item)
			}
		}
	}
}

func TestWorkspaceDiagnose_BackendAddressUsesParserAndRepairs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "group.hcl")
	writeFile(t, filepath.Join(dir, "module", "main.tf"), `output "id" { value = "x" }`)
	text := `group "service" {
  node "app" {
    source = "./module"
    backend_address = { s3_key_name = "../shared.tfstate" }
  }
}`
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(text))
	diagnostics := ws.Diagnose(context.Background(), path)
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "backend_address.s3_key_name") {
		t.Fatalf("diagnostics = %v, want invalid file name diagnostic", diagnostics)
	}
	if got := text[diagnostics[0].Start:diagnostics[0].End]; got != `{ s3_key_name = "../shared.tfstate" }` {
		t.Fatalf("diagnostic range = %q, want address object", got)
	}
	ws.SetDocument(path, []byte(strings.ReplaceAll(text, "../shared.tfstate", "state.json")))
	if got := ws.Diagnose(context.Background(), path); len(got) != 0 {
		t.Fatalf("diagnostics = %v, want none after repair", got)
	}
}

func TestWorkspaceDiagnose_BackendAddressUseRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(`use "service" {
  as = "checkout"
  source = "./group"
  backend_address = { key = "prod" }
}`))
	if got := ws.Diagnose(context.Background(), path); len(got) != 1 || !strings.Contains(got[0].Message, "unsupported field") {
		t.Fatalf("diagnostics = %v, want unsupported address field", got)
	}
}
