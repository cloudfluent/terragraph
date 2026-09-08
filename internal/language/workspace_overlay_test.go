package language

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspace_GroupExportsFollowOpenDocumentAndClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	group := filepath.Join(dir, "g", "group.hcl")
	old := `group "g" {
 export {
  output "old" { from = node.a.output.id }
 }
}`
	writeFile(t, group, old)
	text := "use \"g\" {\n as = \"g\"\n source = \"./g\"\n}\nedge {\n from = use.g.output.\n}"
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(text))
	ws.SetDocument(group, []byte(strings.ReplaceAll(old, "\"old\"", "\"fresh\"")))
	offset := strings.Index(text, "use.g.output.") + len("use.g.output.")
	got := ws.Complete(context.Background(), path, offset)
	if !contains(got, "fresh") || contains(got, "old") {
		t.Fatalf("completion = %#v, want fresh only", got)
	}
	ws.CloseDocument(group)
	got = ws.Complete(context.Background(), path, offset)
	if !contains(got, "old") || contains(got, "fresh") {
		t.Fatalf("completion after close = %#v, want disk export old", got)
	}
	disk, err := os.ReadFile(group)
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != old {
		t.Fatalf("disk group = %q, want unchanged %q", disk, old)
	}
}

func TestWorkspace_UnsavedSiblingParticipatesInFileScope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	sibling := filepath.Join(dir, "group.hcl")
	ws := NewWorkspace(dir)
	text := "edge {\n from = node.fresh\n to = node.fresh\n}"
	ws.SetDocument(path, []byte(text))
	ws.SetDocument(sibling, []byte(`node "fresh" { source = "./m" }`))
	offset := strings.Index(text, "node.fresh") + len("node.")
	if got := ws.Complete(context.Background(), path, offset); !contains(got, "fresh") {
		t.Fatalf("completion = %#v, want fresh", got)
	}
	if got, ok := ws.Definition(context.Background(), path, offset+1); !ok || got.Path != sibling {
		t.Fatalf("definition = %#v (%v), want sibling overlay", got, ok)
	}
	if got := ws.Diagnose(context.Background(), path); len(got) != 0 {
		t.Fatalf("diagnostics = %#v, want none", got)
	}
}
