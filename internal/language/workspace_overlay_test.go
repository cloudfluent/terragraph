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

func checkLockFileExcludedFromScope(t *testing.T, unsaved bool) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nodes.hcl")
	lock := filepath.Join(dir, ".terraform.lock.hcl")
	writeFile(t, filepath.Join(dir, ".visible.hcl"), `node "visible" { source = "./m" }`)
	ws := NewWorkspace(dir)
	contents := []byte(`node "ignored" { source = "./m" }`)
	if unsaved {
		ws.SetDocument(lock, contents)
	} else {
		writeFile(t, lock, string(contents))
	}
	text := "edge {\n from = node.ignored\n to = node.visible\n}"
	ws.SetDocument(path, []byte(text))
	offset := strings.Index(text, "node.ignored") + len("node.")
	if got := ws.Complete(context.Background(), path, offset); contains(got, "ignored") || !contains(got, "visible") {
		t.Fatalf("got = %#v, want visible without ignored", got)
	}
	if got, ok := ws.Definition(context.Background(), path, offset+1); ok {
		t.Fatalf("got = %#v, want no definition from lock file", got)
	}
}

func TestWorkspace_LockFileExcludedFromDiskScope(t *testing.T) {
	checkLockFileExcludedFromScope(t, false)
}

func TestWorkspace_LockFileExcludedFromUnsavedScope(t *testing.T) {
	checkLockFileExcludedFromScope(t, true)
}

func TestWorkspace_ActiveLockFileDoesNotReenterScope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".terraform.lock.hcl")
	ws := NewWorkspace(dir)
	text := "node \"ignored\" { source = \"./m\" }\nedge {\n from = node.\n}"
	ws.SetDocument(path, []byte(text))
	offset := strings.LastIndex(text, "node.") + len("node.")
	if got := ws.Complete(context.Background(), path, offset); contains(got, "ignored") {
		t.Fatalf("got = %#v, want no node from active lock file", got)
	}
}

func TestWorkspace_ExplicitNonHCLDocumentRetainsSymbols(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topology.config")
	ws := NewWorkspace(dir)
	text := "node \"visible\" { source = \"./m\" }\nedge {\n from = node.\n}"
	ws.SetDocument(path, []byte(text))
	offset := strings.LastIndex(text, "node.") + len("node.")
	if got := ws.Complete(context.Background(), path, offset); !contains(got, "visible") {
		t.Fatalf("got = %#v, want explicit document's node", got)
	}
}

func TestWorkspace_GroupSourceExcludesLockFiles(t *testing.T) {
	for _, unsaved := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "nodes.hcl")
		group := filepath.Join(dir, "g", "components.hcl")
		lock := filepath.Join(dir, "g", ".terraform.lock.hcl")
		definition := `group "g" {
 export {
  output "visible" { from = node.a.output.id }
 }
}`
		writeFile(t, group, definition)
		ws := NewWorkspace(dir)
		ignored := strings.ReplaceAll(definition, "visible", "ignored")
		if unsaved {
			ws.SetDocument(lock, []byte(ignored))
		} else {
			writeFile(t, lock, ignored)
		}
		text := "use \"g\" {\n as = \"g\"\n source = \"./g\"\n}\nedge {\n from = use.g.output.\n}"
		ws.SetDocument(path, []byte(text))
		offset := strings.Index(text, "use.g.output.") + len("use.g.output.")
		if got := ws.Complete(context.Background(), path, offset); contains(got, "ignored") || !contains(got, "visible") {
			t.Fatalf("got = %#v, want visible group output without ignored (unsaved=%v)", got, unsaved)
		}
	}
}
