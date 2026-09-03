package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// writeEngineContractsFixture mirrors writeContractsFixture from the graph package at engine level: blueprint.hcl, two modules, one data edge, optional contracts.hcl beside the blueprint — which is the file engine load must find on its own.
func writeEngineContractsFixture(t *testing.T, contractsHCL string) (*Engine, error) {
	t.Helper()
	root := t.TempDir()
	if err := osWriteFile(filepath.Join(root, "modules", "vpc", "main.tf"), []byte(`output "vpc_id" { value = "x" }`)); err != nil {
		t.Fatalf("writing vpc module: %v", err)
	}
	if err := osWriteFile(filepath.Join(root, "modules", "app", "main.tf"), []byte("variable \"vpc_id\" { type = string }\noutput \"ok\" { value = var.vpc_id }\n")); err != nil {
		t.Fatalf("writing app module: %v", err)
	}
	bp := "node \"vpc\" { source = \"./modules/vpc\" }\nnode \"app\" { source = \"./modules/app\" }\nedge {\n  from = node.vpc.output.vpc_id\n  to   = node.app.input.vpc_id\n}\n"
	if err := osWriteFile(filepath.Join(root, "blueprint.hcl"), []byte(bp)); err != nil {
		t.Fatalf("writing blueprint: %v", err)
	}
	if contractsHCL != "" {
		if err := osWriteFile(filepath.Join(root, "contracts.hcl"), []byte(contractsHCL)); err != nil {
			t.Fatalf("writing contracts: %v", err)
		}
	}
	return Load(filepath.Join(root, "blueprint.hcl"), exec.Terraform, &bytes.Buffer{}, &bytes.Buffer{})
}

// osWriteFile is os.WriteFile with mkdir -p, matching how every other engine fixture writes trees without importing the graph package's test helpers (test files are package-private and cannot be shared across packages).
func osWriteFile(path string, data []byte) error {
	if err := osMkdirAll(filepath.Dir(path)); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func osMkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }

// TestLoad_AttachesContractsBesideBlueprint proves engine load finds contracts.hcl next to the blueprint with no flag, and that a mismatched contract reaches users as a warning through the ordinary Validate path.
func TestLoad_AttachesContractsBesideBlueprint(t *testing.T) {
	e, err := writeEngineContractsFixture(t, `
producer "./modules/vpc" {
  output "vpc_id" { type = "list(string)" }
}
consumer "./modules/app" {
  input "vpc_id" { type = "string" }
}
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if e.Graph.Contracts == nil {
		t.Fatal("engine load did not attach contracts.hcl sitting next to the blueprint")
	}
	found := false
	for _, p := range e.Validate() {
		if strings.Contains(p.Message, "[C003]") {
			found = true
			if p.IsError() {
				t.Fatalf("phase-1 contract problems must be warnings, got error: %s", p.Message)
			}
		}
	}
	if !found {
		t.Fatal("expected a C003 type-incompatibility warning from Validate")
	}
}

// TestLoad_NoContractsFileIsLegacy proves a directory without contracts.hcl loads exactly as before: nil Contracts, no new problems.
func TestLoad_NoContractsFileIsLegacy(t *testing.T) {
	e, err := writeEngineContractsFixture(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if e.Graph.Contracts != nil {
		t.Fatal("contracts attached although no contracts.hcl exists")
	}
	if problems := e.Validate(); len(problems) != 0 {
		t.Fatalf("got = %v, want zero problems", problems)
	}
}

// TestLoad_EnforceModeBlocksValidate proves the mode travels blueprint -> engine -> graph and flips checkValidate's verdict: with enforce, a C003 contract violation fails validation exactly like a structural error would.
func TestLoad_EnforceModeBlocksValidate(t *testing.T) {
	root := t.TempDir()
	for path, data := range map[string][]byte{
		"modules/vpc/main.tf": []byte(`output "vpc_id" { value = "x" }`),
		"modules/app/main.tf": []byte("variable \"vpc_id\" {\n  type = string\n}\n"),
		"blueprint.hcl":       []byte("contracts {\n  mode = \"enforce\"\n}\n\nnode \"vpc\" {\n  source = \"./modules/vpc\"\n}\n\nnode \"app\" {\n  source = \"./modules/app\"\n}\n\nedge {\n  from = node.vpc.output.vpc_id\n  to   = node.app.input.vpc_id\n}\n"),
		"contracts.hcl":       []byte("producer \"./modules/vpc\" {\n  output \"vpc_id\" {\n    type = \"list(string)\"\n  }\n}\n\nconsumer \"./modules/app\" {\n  input \"vpc_id\" {\n    type = \"string\"\n  }\n}\n"),
	} {
		if err := osWriteFile(filepath.Join(root, path), data); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
	e, err := Load(filepath.Join(root, "blueprint.hcl"), exec.Terraform, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	blocked := false
	for _, p := range e.Validate() {
		if strings.Contains(p.Message, "[C003]") && p.IsError() {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("enforce mode did not escalate C003 to an error")
	}
}
