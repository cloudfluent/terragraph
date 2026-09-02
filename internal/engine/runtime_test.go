package engine

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func TestRuntimeFor_NodeExplicitWinsOverBlueprintDefaultAndCLI(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
runtime "tofu" {
  binary  = "tofu"
  version = ">= 1.8.0"
}
runtime "legacy" {
  binary  = "terraform"
  default = true
}

node "vpc" {
  source  = "./stacks/vpc"
  runtime = runtime.tofu
}
`)

	// CLI selected plain "terraform", but the node's own explicit runtime.tofu must still win.
	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	rt := e.runtimeFor("vpc")
	if rt.Binary != "tofu" || rt.Version != ">= 1.8.0" {
		t.Fatalf("unexpected runtime: %+v", rt)
	}
}

func TestRuntimeFor_BlueprintDefaultAppliesWhenNodeHasNone(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
runtime "legacy" {
  binary  = "/opt/terraform_1.5.7"
  default = true
}

node "vpc" { source = "./stacks/vpc" }
`)

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	rt := e.runtimeFor("vpc")
	if rt.Binary != "/opt/terraform_1.5.7" {
		t.Fatalf("expected the blueprint's default runtime to apply, got %+v", rt)
	}
}

func TestRuntimeFor_CLIBinaryIsLastResortFallback(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
node "vpc" { source = "./stacks/vpc" }
`)

	e, err := Load(path, exec.OpenTofu, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	rt := e.runtimeFor("vpc")
	if rt.Binary != exec.OpenTofu || rt.Version != "" {
		t.Fatalf("expected the CLI-selected binary as a last resort, got %+v", rt)
	}
}

func TestValidate_WarnsWhenSharedSourceNodesResolveToDifferentRuntimes(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
runtime "tofu" { binary = "tofu" }

node "vpc_a" {
  source          = "./stacks/vpc"
  backend_config  = { path = "a.tfstate" }
  runtime         = runtime.tofu
}
node "vpc_b" {
  source         = "./stacks/vpc"
  backend_config = { path = "b.tfstate" }
}
`)

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var found bool
	for _, p := range e.Validate() {
		if p.IsError() {
			t.Fatalf("unexpected error-level problem: %s", p.Message)
		}
		if strings.Contains(p.Message, "vpc_a") && strings.Contains(p.Message, "vpc_b") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning about vpc_a/vpc_b sharing a source directory under different runtimes")
	}
}

func TestValidate_NoWarningWhenSharedSourceNodesAgreeOnRuntime(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
node "vpc_a" {
  source         = "./stacks/vpc"
  backend_config = { path = "a.tfstate" }
}
node "vpc_b" {
  source         = "./stacks/vpc"
  backend_config = { path = "b.tfstate" }
}
`)

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, p := range e.Validate() {
		if strings.Contains(p.Message, "conflict") {
			t.Fatalf("did not expect a runtime conflict warning when both nodes agree, got: %s", p.Message)
		}
	}
}

func TestResolvedRuntime_CacheIdentityDistinguishesBinaryAndVersion(t *testing.T) {
	a := resolvedRuntime{Binary: exec.Terraform, Version: ">= 1.8.0"}
	b := resolvedRuntime{Binary: exec.OpenTofu, Version: ">= 1.8.0"}
	c := resolvedRuntime{Binary: exec.Terraform, Version: ">= 1.9.0"}

	if a.cacheIdentity() == b.cacheIdentity() {
		t.Fatalf("expected distinct binaries to produce distinct cache identities")
	}
	if a.cacheIdentity() == c.cacheIdentity() {
		t.Fatalf("expected distinct declared versions to produce distinct cache identities")
	}
}
