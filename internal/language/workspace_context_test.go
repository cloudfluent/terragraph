package language

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func TestWorkspace_GroupReferencesUseTheirOwnScope(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "inner", "main.tf"), "variable \"inner_id\" { type = string }\noutput \"inner_id\" { value = \"ok\" }")
	writeFile(t, filepath.Join(dir, "outer", "main.tf"), "output \"outer_id\" { value = \"ok\" }")
	path := filepath.Join(dir, "group.hcl")
	text := `node "same" { source = "./outer" }
group "g" {
 node "same" { source = "./inner" }
 export {
  input "id" { to = node.same.input.inner_id }
  output "id" { from = node.same.output.inner_id }
 }
}`
	writeFile(t, path, text)
	if _, err := blueprint.ParseFile(path); err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(dir)
	if got := ws.Diagnose(context.Background(), path); len(got) != 0 {
		t.Errorf("diagnostics = %#v, want none", got)
	}
	offset := strings.Index(text, "node.same.output.") + len("node.same.output.")
	if got := ws.Complete(context.Background(), path, offset); !contains(got, "inner_id") || contains(got, "outer_id") {
		t.Errorf("completion = %#v, want inner_id only", got)
	}
	target, ok := ws.Definition(context.Background(), path, offset-len(".output.")-1)
	want := strings.Index(text, "node \"same\" { source = \"./inner\"") + len("node ")
	if !ok || target.Start != want {
		t.Errorf("definition = %#v (%v), want label at %d", target, ok, want)
	}
}

func TestWorkspace_CommentsAndStringsAreNotReferences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	text := `# node.comment.output.x
node "a" {
 source = "./m"
 env = {
  NOTE = "node.literal.output.id"
  DOC = <<-TEXT
node.heredoc.output.id { }
TEXT
 }
}`
	writeFile(t, path, text)
	if _, err := blueprint.ParseFile(path); err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace(dir)
	if got := ws.Diagnose(context.Background(), path); len(got) != 0 {
		t.Fatalf("diagnostics = %#v, want none", got)
	}
}

func TestWorkspace_StringBraceDoesNotChangeCompletionScope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	text := `node "a" {
 source = "./m"
 env = { NOTE = "}" }
 
}`
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(text))
	if got := ws.Complete(context.Background(), path, strings.LastIndex(text, "\n ")+2); !contains(got, "source") || contains(got, "node") {
		t.Fatalf("completion = %#v, want node attributes", got)
	}
}

func TestWorkspace_InlineEdgeDirectionIsDiagnosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	writeFile(t, filepath.Join(dir, "m", "main.tf"), "variable \"id\" { type = string }\noutput \"id\" { value = \"ok\" }")
	text := "node \"a\" { source = \"./m\" }\nedge { from = node.a.input.id\n to = node.a.input.id\n}"
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(text))
	got := ws.Diagnose(context.Background(), path)
	if len(got) != 1 || !strings.Contains(got[0].Message, "from must reference node output") {
		t.Fatalf("diagnostics = %#v, want wrong direction", got)
	}
}

func TestWorkspace_GroupAndSnapshotsCompletionContexts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "group.hcl")
	ws := NewWorkspace(dir)
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"group \"g\" {\n __CURSOR__\n}", []string{"node", "edge", "use", "export"}},
		{"group \"g\" {\n export {\n __CURSOR__\n }\n}", []string{"input", "output"}},
		{"group \"g\" {\n export {\n input \"id\" {\n __CURSOR__\n }\n }\n}", []string{"to"}},
		{"group \"g\" {\n export {\n output \"id\" {\n __CURSOR__\n }\n }\n}", []string{"from"}},
		{"snapshots {\n __CURSOR__\n}", nil},
	} {
		text, offset := cursor(tc.text, "__CURSOR__")
		ws.SetDocument(path, []byte(text))
		got := ws.Complete(context.Background(), path, offset)
		if len(got) != len(tc.want) {
			t.Errorf("%s: completion = %#v, want %v", tc.text, got, tc.want)
			continue
		}
		for _, want := range tc.want {
			if !contains(got, want) {
				t.Errorf("%s: completion = %#v, want %s", tc.text, got, want)
			}
		}
	}
}

func TestWorkspace_UnresolvedSchemaDoesNotInventUnknownPorts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	text := `node "remote" { source = "git::https://example.invalid/module" }
node "local" { source = "./missing" }
edge {
 from = node.remote.output.id
 to = node.local.input.id
}`
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(text))
	if got := ws.Diagnose(context.Background(), path); len(got) != 0 {
		t.Fatalf("diagnostics = %#v, want none for unresolved schemas", got)
	}
}

func TestWorkspace_GroupEdgeInputsAndVarsUseLocalNodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "group.hcl")
	writeFile(t, filepath.Join(dir, "m", "main.tf"), "variable \"id\" { type = string }\noutput \"id\" { value = \"ok\" }")
	text := `group "g" {
 node "a" { source = "./m" }
 node "b" {
  source = "./m"
  vars = { typo = "x" }
 }
 edge {
  from = node.a
  to = node.b
  input "missing" { from = output.absent }
 }
}`
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(text))
	got := ws.Diagnose(context.Background(), path)
	if len(got) != 3 {
		t.Fatalf("diagnostics = %#v, want invalid vars, input and output", got)
	}
	for _, d := range got {
		if strings.Contains(d.Message, "Unknown node") {
			t.Fatalf("diagnostics = %#v, want resolved group nodes", got)
		}
	}
}
