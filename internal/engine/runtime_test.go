package engine

import (
	"io"
	"os"
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

	if rt := e.runtimeFor("vpc"); rt != "tofu" {
		t.Fatalf("unexpected runtime: %q", rt)
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

	if rt := e.runtimeFor("vpc"); rt != "/opt/terraform_1.5.7" {
		t.Fatalf("expected the blueprint's default runtime to apply, got %q", rt)
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

	if rt := e.runtimeFor("vpc"); rt != exec.OpenTofu {
		t.Fatalf("expected the CLI-selected binary as a last resort, got %q", rt)
	}
}

func TestLoad_CLIOpenTofuSelectsTofuSchema(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "module"))
	if err := os.WriteFile(filepath.Join(baseDir, "module", "main.tofu"), []byte(`output "tofu_only" { value = "x" }`), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := Load(writeBlueprint(t, baseDir, `node "a" { source = "./module" }`), exec.OpenTofu, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !e.Graph.Nodes["a"].Schema.HasOutput("tofu_only") {
		t.Fatal("CLI OpenTofu did not select main.tofu")
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

func TestEnvFor_UseOverrideCascadesToNode(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
use "g" {
  as     = "inst"
  source = "./groups/g"
  env = {
    AWS_PROFILE = "prod"
  }
}
`)
	if err := os.MkdirAll(filepath.Join(baseDir, "groups", "g"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeBlueprint(t, filepath.Join(baseDir, "groups", "g"), `
group "g" {
  node "vpc" { source = "../../stacks/vpc" }
}
`)

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	env := e.envFor("inst.vpc")
	if env["AWS_PROFILE"] != "prod" {
		t.Fatalf("expected inst.vpc to inherit the use block's env, got %+v", env)
	}
}

func TestLoad_RuntimePathsRemainUnambiguousWithoutTofuDifferences(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, binary := range []string{"/opt/homebrew/bin/tofu", "/opt/terraform_1.5.7", "runtime-wrapper"} {
		t.Run(binary, func(t *testing.T) {
			dir := t.TempDir()
			writeModule(t, filepath.Join(dir, "module"))
			path := writeBlueprint(t, dir, `node "a" { source = "./module" }`)
			if _, err := Load(path, exec.Binary(binary), io.Discard, io.Discard); err != nil {
				t.Fatalf("ordinary module with %s: %v", binary, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "module", "extra.tofu"), []byte(`output "tofu_only" { value = 1 }`), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path, exec.Binary(binary), io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "runtime binary is ambiguous") {
				t.Fatalf("runtime %s error = %v, want ambiguity diagnostic", binary, err)
			}
		})
	}
}

func TestLoad_BackendOverrideMatchesResolvedRuntime(t *testing.T) {
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "module")
	if err := os.Mkdir(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	local := "terraform {\n backend \"local\" {}\n}\n"
	cloud := "terraform {\n cloud { organization = \"offline-fixture\" }\n}\n"
	for name, source := range map[string]string{"main.tf": local, "override.tf": cloud, "main.tofu": cloud, "override.tofu": local} {
		if err := os.WriteFile(filepath.Join(moduleDir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := writeBlueprint(t, dir, `node "a" { source = "./module" }`)
	terraform, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if node := terraform.Graph.Nodes["a"]; node.Schema.Backend != "cloud" || len(node.BackendConfig) != 0 {
		t.Fatalf("Terraform cloud node got backend %q and local overrides %v", node.Schema.Backend, node.BackendConfig)
	}
	tofu, err := Load(path, exec.OpenTofu, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if node := tofu.Graph.Nodes["a"]; node.Schema.Backend != "local" || node.BackendConfig["path"] != filepath.Join(dir, ".terragraph", "state", "a.tfstate") {
		t.Fatalf("OpenTofu local node got backend %q and overrides %v", node.Schema.Backend, node.BackendConfig)
	}
	if _, err := Load(path, exec.Binary("offline-wrapper"), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "runtime binary is ambiguous") {
		t.Fatalf("unknown runtime Load = %v, want ambiguity diagnostic", err)
	}
}
