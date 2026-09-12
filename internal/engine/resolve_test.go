package engine

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// TestResolveRuntime_OriginPrecedence walks the whole runtime ladder against one
// fixture with real files, so every Decl/Def location is assertable: the node's
// own runtime attribute, the enclosing use's, and the root blueprint's
// Default-marked block each report their own origin and the exact line that
// selected them plus the runtime block that defined what was selected.
func TestResolveRuntime_OriginPrecedence(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
runtime "pinned" {
  binary  = "tofu"
  version = ">= 1.8.0"
}
runtime "legacy" {
  binary  = "/opt/terraform_1.5.7"
  default = true
}

node "own" {
  source  = "./stacks/vpc"
  runtime = runtime.pinned
}

use "g" {
  as      = "inst"
  source  = "./groups/g"
  runtime = runtime.pinned
}

node "bare" { source = "./stacks/vpc" }
`)
	groupDir := filepath.Join(baseDir, "groups", "g")
	if err := os.MkdirAll(groupDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", groupDir, err)
	}
	writeBlueprint(t, groupDir, `
group "g" {
  node "leaf" { source = "../../stacks/vpc" }
}
`)

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cases := []struct {
		label string
		node  string
		want  RuntimeResolution
	}{
		{
			label: "node attribute wins",
			node:  "own",
			want: RuntimeResolution{
				Binary: "tofu", Origin: RuntimeOriginNode,
				RuntimeName: "pinned", Version: ">= 1.8.0",
				Decl: blueprint.Loc{File: path, Line: 13, Column: 3},
				Def:  blueprint.Loc{File: path, Line: 2, Column: 1},
			},
		},
		{
			label: "enclosing use supplies",
			node:  "inst.leaf",
			want: RuntimeResolution{
				Binary: "tofu", Origin: RuntimeOriginUse,
				RuntimeName: "pinned", Version: ">= 1.8.0",
				Decl:    blueprint.Loc{File: path, Line: 19, Column: 3},
				Def:     blueprint.Loc{File: path, Line: 2, Column: 1},
				UseName: "inst", UseDecl: blueprint.Loc{File: path, Line: 16, Column: 1},
			},
		},
		{
			label: "root default runtime",
			node:  "bare",
			want: RuntimeResolution{
				Binary: "/opt/terraform_1.5.7", Origin: RuntimeOriginRoot,
				RuntimeName: "legacy",
				Decl:        blueprint.Loc{File: path, Line: 6, Column: 1},
				Def:         blueprint.Loc{File: path, Line: 6, Column: 1},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := e.ResolveRuntime(tc.node, false)
			if got != tc.want {
				t.Fatalf("ResolveRuntime(%q) = %+v, want %+v", tc.node, got, tc.want)
			}
		})
	}
}

// TestResolveRuntime_CLIExplicitVsBuiltinOrigins pins the residual layer's two
// indistinguishable-at-execution origins apart: e.Binary is the same field
// whether the CLI's --tofu flag set it or nothing did, so only the caller's
// explicit-flag context separates "cli" from the built-in terraform default —
// and neither row may invent a runtime name, version, or location.
func TestResolveRuntime_CLIExplicitVsBuiltinOrigins(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `node "bare" { source = "./stacks/vpc" }`)

	explicit, err := Load(path, exec.OpenTofu, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := explicit.ResolveRuntime("bare", true)
	want := RuntimeResolution{Binary: exec.OpenTofu, Origin: RuntimeOriginCLI}
	if got != want {
		t.Fatalf("explicit --tofu: got %+v, want %+v", got, want)
	}

	builtin, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got = builtin.ResolveRuntime("bare", false)
	want = RuntimeResolution{Binary: exec.Terraform, Origin: RuntimeOriginBuiltin}
	if got != want {
		t.Fatalf("built-in default: got %+v, want %+v", got, want)
	}
}

// TestResolveApprove_OriginPrecedence walks the approve ladder: node and use
// declarations, the explicit --approve layer, and the built-in safe — including
// the two rules #94 accepts: an explicit --approve safe and the implicit
// built-in safe are the same policy through different origins, and --approve
// all never overrides a declared safe.
func TestResolveApprove_OriginPrecedence(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
group "svc" {
  node "worker" { source = "./stacks/vpc" }
}

use "svc" {
  as      = "prod"
  source  = "."
  approve = "safe"
}

node "cautious" {
  source  = "./stacks/vpc"
  approve = "safe"
}

node "guarded" {
  source  = "./stacks/vpc"
  approve = "none"
}

node "plain" { source = "./stacks/vpc" }
`)

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	cases := []struct {
		label       string
		node        string
		cliExplicit bool
		cliValue    blueprint.Approve
		want        ApproveResolution
	}{
		{
			label:       "declared node safe survives --approve all",
			node:        "cautious",
			cliExplicit: true,
			cliValue:    blueprint.ApproveAll,
			want: ApproveResolution{
				Policy: blueprint.ApproveSafe, Origin: ApproveOriginNode,
				Decl: blueprint.Loc{File: path, Line: 14, Column: 3},
			},
		},
		{
			label:       "declared node none survives --approve all",
			node:        "guarded",
			cliExplicit: true,
			cliValue:    blueprint.ApproveAll,
			want: ApproveResolution{
				Policy: blueprint.ApproveNone, Origin: ApproveOriginNode,
				Decl: blueprint.Loc{File: path, Line: 19, Column: 3},
			},
		},
		{
			label:       "use-inherited safe survives --approve all",
			node:        "prod.worker",
			cliExplicit: true,
			cliValue:    blueprint.ApproveAll,
			want: ApproveResolution{
				Policy: blueprint.ApproveSafe, Origin: ApproveOriginUse,
				Decl:    blueprint.Loc{File: path, Line: 9, Column: 3},
				UseName: "prod", UseDecl: blueprint.Loc{File: path, Line: 6, Column: 1},
			},
		},
		{
			label:       "explicit --approve safe is the cli layer",
			node:        "plain",
			cliExplicit: true,
			cliValue:    blueprint.ApproveSafe,
			want:        ApproveResolution{Policy: blueprint.ApproveSafe, Origin: ApproveOriginCLI},
		},
		{
			label:       "omitted --approve is the builtin layer",
			node:        "plain",
			cliExplicit: false,
			want:        ApproveResolution{Policy: blueprint.ApproveSafe, Origin: ApproveOriginBuiltin},
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got := e.ResolveApprove(tc.node, tc.cliExplicit, tc.cliValue)
			if got != tc.want {
				t.Fatalf("ResolveApprove(%q, %t, %q) = %+v, want %+v", tc.node, tc.cliExplicit, tc.cliValue, got, tc.want)
			}
		})
	}
}
