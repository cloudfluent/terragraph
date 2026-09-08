package language

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func runtimeWorkspace(t *testing.T, text string) (*Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "m", "main.tf"), "variable \"tf_id\" { type = string }\noutput \"tf_id\" { value = \"tf\" }")
	writeFile(t, filepath.Join(dir, "m", "main.tofu"), "variable \"tofu_id\" { type = string }\noutput \"tofu_id\" { value = \"tofu\" }")
	path := filepath.Join(dir, "blueprint.hcl")
	ws := NewWorkspace(dir)
	ws.SetDocument(path, []byte(text))
	return ws, path
}

func TestWorkspace_RuntimeSelectionSeparatesSharedSourceNodes(t *testing.T) {
	text := `node "tf" { source = "./m" }
node "tofu" {
 source = "./m"
 runtime = runtime.tofu
}
edge {
 from = node.tofu.output.tofu_id
 to = node.tf.input.tf_id
}
runtime "tofu" { binary = "tofu" }
`
	ws, path := runtimeWorkspace(t, text)
	if got := ws.Diagnose(context.Background(), path); len(got) != 0 {
		t.Fatalf("diagnostics = %#v, want none", got)
	}
	for _, tc := range []struct{ ref, want, absent string }{
		{"node.tofu.output.", "tofu_id", "tf_id"},
		{"node.tf.input.", "tf_id", "tofu_id"},
	} {
		offset := strings.Index(text, tc.ref) + len(tc.ref)
		got := ws.Complete(context.Background(), path, offset)
		if !contains(got, tc.want) || contains(got, tc.absent) {
			t.Fatalf("%s completion = %#v, want %s only", tc.ref, got, tc.want)
		}
	}
}

func TestWorkspace_DefaultRuntimeUsesUnsavedSiblingDeclaration(t *testing.T) {
	text := `node "a" { source = "./m" }
edge {
 from = node.a.output.tofu_id
 to = node.a.input.tofu_id
}`
	ws, path := runtimeWorkspace(t, text)
	ws.SetDocument(filepath.Join(filepath.Dir(path), "z-runtime.hcl"), []byte("runtime \"tool\" {\n binary = \"tofu\"\n default = true\n}"))
	if got := ws.Diagnose(context.Background(), path); len(got) != 0 {
		t.Fatalf("diagnostics = %#v, want none", got)
	}
	offset := strings.Index(text, "node.a.output.") + len("node.a.output.")
	if got := ws.Complete(context.Background(), path, offset); !contains(got, "tofu_id") || contains(got, "tf_id") {
		t.Fatalf("completion = %#v, want tofu_id only", got)
	}
}

func TestWorkspace_StandaloneGroupDoesNotInheritDirectoryDefault(t *testing.T) {
	text := `runtime "tool" {
 binary = "terraform"
 default = true
}
group "g" {
 node "a" { source = "./m" }
 export {
  output "id" { from = node.a.output.tofu_id }
 }
}`
	ws, path := runtimeWorkspace(t, text)
	if got := ws.Diagnose(context.Background(), path); len(got) != 0 {
		t.Fatalf("diagnostics = %#v, want none for unresolved group runtime", got)
	}
	offset := strings.Index(text, "node.a.output.") + len("node.a.output.")
	if got := ws.Complete(context.Background(), path, offset); len(got) != 0 {
		t.Fatalf("completion = %#v, want no invented group schema", got)
	}
	text = strings.Replace(text, `node "a" { source = "./m" }`, "node \"a\" {\n source = \"./m\"\n runtime = runtime.tool\n}", 1)
	text = strings.ReplaceAll(text, "tofu_id", "tf_id")
	ws.SetDocument(path, []byte(text))
	if got := ws.Diagnose(context.Background(), path); len(got) != 0 {
		t.Fatalf("explicit group runtime diagnostics = %#v, want none", got)
	}
	offset = strings.Index(text, "node.a.output.") + len("node.a.output.")
	if got := ws.Complete(context.Background(), path, offset); !contains(got, "tf_id") {
		t.Fatalf("explicit group runtime completion = %#v, want tf_id", got)
	}
}

func TestWorkspace_AmbiguousRuntimeDoesNotInventUnknownPorts(t *testing.T) {
	text := `runtime "tool" { binary = "./runtime-wrapper" }
node "a" {
 source = "./m"
 runtime = runtime.tool
 vars = { tofu_id = "value" }
}
edge {
 from = node.a.output.tofu_id
 to = node.a.input.tofu_id
}`
	ws, path := runtimeWorkspace(t, text)
	if got := ws.Diagnose(context.Background(), path); len(got) != 0 {
		t.Fatalf("diagnostics = %#v, want none for ambiguous runtime", got)
	}
}
