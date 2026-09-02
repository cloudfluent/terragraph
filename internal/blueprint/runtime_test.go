package blueprint

import "testing"

func TestParseFile_RuntimeBlockAllFields(t *testing.T) {
	path := writeTemp(t, `
runtime "tofu" {
  binary  = "tofu"
  version = ">= 1.8.0"
  default = true
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Runtimes) != 1 {
		t.Fatalf("expected 1 runtime, got %d", len(bp.Runtimes))
	}
	rt := bp.Runtimes[0]
	if rt.Name != "tofu" || rt.Binary != "tofu" || rt.Version != ">= 1.8.0" || !rt.Default {
		t.Fatalf("unexpected runtime: %+v", rt)
	}

	def, ok := bp.DefaultRuntime()
	if !ok || def.Name != "tofu" {
		t.Fatalf("DefaultRuntime() = %+v, %v; want tofu, true", def, ok)
	}
}

func TestParseFile_RuntimeBlockBinaryRequired(t *testing.T) {
	path := writeTemp(t, `
runtime "tofu" {
  version = ">= 1.8.0"
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a runtime block missing binary")
	}
}

func TestParseFile_RuntimeBlockEmptyBinaryRejected(t *testing.T) {
	path := writeTemp(t, `
runtime "tofu" {
  binary = ""
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a runtime block with an empty binary")
	}
}

func TestParseFile_DuplicateRuntimeNameRejected(t *testing.T) {
	path := writeTemp(t, `
runtime "tofu" { binary = "tofu" }
runtime "tofu" { binary = "tofu2" }
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a duplicate runtime name")
	}
}

func TestParseFile_MultipleDefaultRuntimesRejected(t *testing.T) {
	path := writeTemp(t, `
runtime "a" {
  binary  = "terraform"
  default = true
}
runtime "b" {
  binary  = "tofu"
  default = true
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for more than one default runtime")
	}
}

func TestParseFile_NodeRuntimeReference(t *testing.T) {
	path := writeTemp(t, `
runtime "tofu" { binary = "tofu" }

node "a" {
  source  = "./a"
  runtime = runtime.tofu
}
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Nodes[0].Runtime != "tofu" {
		t.Fatalf("expected node runtime %q, got %q", "tofu", bp.Nodes[0].Runtime)
	}
}

func TestParseFile_NodeRuntimeUnknownReferenceRejected(t *testing.T) {
	path := writeTemp(t, `
node "a" {
  source  = "./a"
  runtime = runtime.missing
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a node referencing an undeclared runtime")
	}
}

func TestParseFile_UseRuntimeReference(t *testing.T) {
	path := writeTemp(t, `
runtime "legacy" { binary = "/opt/terraform_1.5.7" }

use "g" {
  as      = "inst"
  source  = "./groups/g"
  runtime = runtime.legacy
}
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Uses[0].Runtime != "legacy" {
		t.Fatalf("expected use runtime %q, got %q", "legacy", bp.Uses[0].Runtime)
	}
}

func TestParseFile_UseRuntimeUnknownReferenceRejected(t *testing.T) {
	path := writeTemp(t, `
use "g" {
  as      = "inst"
  source  = "./groups/g"
  runtime = runtime.missing
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a use block referencing an undeclared runtime")
	}
}

func TestParseFile_RuntimeMalformedReferenceRejected(t *testing.T) {
	path := writeTemp(t, `
runtime "tofu" { binary = "tofu" }

node "a" {
  source  = "./a"
  runtime = "tofu"
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a runtime attribute that isn't a runtime.<name> reference")
	}
}

func TestParseFile_GroupInternalNodeRuntimeScopedToGroupOwnRuntimes(t *testing.T) {
	// A group body can declare and reference its own runtime blocks; those names are meaningless
	// outside the group (see graph.loadGroupDef), but within the same parse unit they must resolve
	// exactly like a top-level node's would.
	path := writeTemp(t, `
runtime "grouplocal" { binary = "grouplocal-binary" }

group "g" {
  node "a" {
    source  = "./a"
    runtime = runtime.grouplocal
  }
}
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Groups[0].Nodes[0].Runtime != "grouplocal" {
		t.Fatalf("expected group-internal node runtime %q, got %q", "grouplocal", bp.Groups[0].Nodes[0].Runtime)
	}
}

func TestParseFile_GroupInternalNodeRuntimeUnknownReferenceRejected(t *testing.T) {
	path := writeTemp(t, `
group "g" {
  node "a" {
    source  = "./a"
    runtime = runtime.missing
  }
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a group-internal node referencing an undeclared runtime")
	}
}

func TestParseFile_NoRuntimeBlockDefaultRuntimeIsAbsent(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if _, ok := bp.DefaultRuntime(); ok {
		t.Fatalf("expected no default runtime when none is declared")
	}
	if _, ok := bp.RuntimeByName("anything"); ok {
		t.Fatalf("expected RuntimeByName to find nothing when no runtime blocks are declared")
	}
}
