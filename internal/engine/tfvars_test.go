package engine

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// writeModule writes a minimal, syntactically valid root module at dir so module.Inspect (static .tf parsing, no terraform binary involved) succeeds.
func writeModule(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	src := "terraform {\n  backend \"local\" {}\n}\noutput \"id\" {\n  value = \"x\"\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing fixture module: %v", err)
	}
}

func writeBlueprint(t *testing.T, baseDir, contents string) string {
	t.Helper()
	path := filepath.Join(baseDir, "blueprint.hcl")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing blueprint fixture: %v", err)
	}
	return path
}

func TestTFVarsPath_DefaultsToWorkdirLocation(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
node "vpc" { source = "./stacks/vpc" }
`)

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := e.tfVarsPath("vpc")
	want := filepath.Join(baseDir, ".terragraph", "vars", "vpc.tfvars.json")
	if got != want {
		t.Fatalf("tfVarsPath() = %q, want %q", got, want)
	}
}

func TestTFVarsPath_ModuleLocation(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
tfvars { location = "module" }

node "vpc" { source = "./stacks/vpc" }
`)

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := e.tfVarsPath("vpc")
	want := filepath.Join(baseDir, "stacks", "vpc", ".terragraph.vpc.tfvars.json")
	if got != want {
		t.Fatalf("tfVarsPath() = %q, want %q", got, want)
	}
}

func TestTFVarsPath_ModuleLocationKeepsSharedSourceApart(t *testing.T) {
	// vpc_a and vpc_b reuse the same module source (see Node.BackendConfig); each must resolve to its own tfvars filename within that shared directory so a parallel run never clobbers the other's values.
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
tfvars { location = "module" }

node "vpc_a" {
  source = "./stacks/vpc"
  backend_config = { path = "a.tfstate" }
}
node "vpc_b" {
  source = "./stacks/vpc"
  backend_config = { path = "b.tfstate" }
}
`)

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	a := e.tfVarsPath("vpc_a")
	b := e.tfVarsPath("vpc_b")
	if a == b {
		t.Fatalf("expected distinct tfvars paths for nodes sharing a source, got %q for both", a)
	}
	if filepath.Dir(a) != filepath.Dir(b) {
		t.Fatalf("expected both paths in the shared module directory, got %q and %q", a, b)
	}
}

func TestTFVarsOrphans_ModuleLocationWarnsAboutStaleFile(t *testing.T) {
	baseDir := t.TempDir()
	moduleDir := filepath.Join(baseDir, "stacks", "vpc")
	writeModule(t, moduleDir)
	path := writeBlueprint(t, baseDir, `
tfvars { location = "module" }

node "vpc" { source = "./stacks/vpc" }
`)

	// Simulates a leftover file from a node that has since been renamed or removed from the blueprint.
	stale := filepath.Join(moduleDir, ".terragraph.vpc_old.tfvars.json")
	if err := os.WriteFile(stale, []byte("{}"), 0o644); err != nil {
		t.Fatalf("writing stale fixture: %v", err)
	}

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	problems := e.Validate()
	var found bool
	for _, p := range problems {
		if p.IsError() {
			t.Fatalf("unexpected error-level problem: %s", p.Message)
		}
		if strings.Contains(p.Message, "vpc_old") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning about the stale %s, got: %+v", filepath.Base(stale), problems)
	}
}

func TestTFVarsOrphans_WorkdirLocationNeverWarns(t *testing.T) {
	baseDir := t.TempDir()
	moduleDir := filepath.Join(baseDir, "stacks", "vpc")
	writeModule(t, moduleDir)
	path := writeBlueprint(t, baseDir, `
node "vpc" { source = "./stacks/vpc" }
`)

	// The workdir location never reads a node's own module directory for tfvars, so an arbitrarily named file sitting there is never terragraph's concern.
	if err := os.WriteFile(filepath.Join(moduleDir, ".terragraph.vpc_old.tfvars.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, p := range e.Validate() {
		if strings.Contains(p.Message, "vpc_old") {
			t.Fatalf("did not expect a tfvars orphan warning under the workdir location, got: %s", p.Message)
		}
	}
}
