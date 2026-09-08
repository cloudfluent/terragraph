package language

import (
	"context"
	"path/filepath"
	"testing"
)

func TestWorkspace_TypedNullSourceKeepsCompletionResponsive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "module", "main.tf"), `variable "name" { type = string }`)
	path := filepath.Join(dir, "blueprint.hcl")
	ws := NewWorkspace(dir)
	text, offset := cursor(`node "bad" { source = true ? null : "./module" }
node "good" {
  source = "./module"
  vars = {
    __CURSOR__
  }
}`, "__CURSOR__")
	ws.SetDocument(path, []byte(text))
	if items := ws.Complete(context.Background(), path, offset); !contains(items, "name") {
		t.Fatalf("completion = %+v, want name despite null in sibling node", items)
	}
	text, offset = cursor(`node "bad" {
  source = "./module"
  vars = {
    __CURSOR__
  }
}`, "__CURSOR__")
	ws.SetDocument(path, []byte(text))
	if items := ws.Complete(context.Background(), path, offset); !contains(items, "name") {
		t.Fatalf("completion after correction = %+v, want name", items)
	}
}
