package graph

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func TestBuild_BackendAddressQualifiedLeavesAndExceptions(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "module"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "group", "group.hcl"), `group "service" {
  node "cluster" { source = "../module" }
  node "database" {
    source = "../module"
    backend_config = { key = "legacy/database.tfstate" }
  }
}`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "service" {
  as = "checkout"
  source = "./group"
  backend_config = { bucket = "checkout", region = "ap-northeast-2" }
  backend_address = { s3_key_prefix = "prod" }
}
use "service" {
  as = "payments"
  source = "./group"
  backend_config = { bucket = "payments", region = "ap-northeast-2" }
  backend_address = { s3_key_prefix = "dev" }
}
node "standalone" {
  source = "./module"
  backend_config = { bucket = "standalone", region = "ap-northeast-2" }
  backend_address = { s3_key_prefix = "prod" }
}
`)
	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, buildGraph := range []func(*blueprint.Blueprint, string, ...string) (*Graph, error){Build, Build, BuildObservation} {
		g, err := buildGraph(bp, root)
		if err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string]string{
			"checkout.cluster":  "prod/checkout.cluster/terraform.tfstate",
			"payments.cluster":  "dev/payments.cluster/terraform.tfstate",
			"checkout.database": "legacy/database.tfstate",
			"payments.database": "legacy/database.tfstate",
			"standalone":        "prod/standalone/terraform.tfstate",
		} {
			if got := g.Nodes[name].BackendConfig["key"]; got != want {
				t.Fatalf("node.%s key = %q, want %q", name, got, want)
			}
		}
		if problems := Validate(g); len(problems) != 0 {
			t.Fatalf("problems = %v, want none", problems)
		}
	}
}

func TestBuild_BackendAddressNestedOverridesAndOptOut(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "module"), s3Backend)
	writeBackendModule(t, filepath.Join(root, "disabled-module"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "inner", "group.hcl"), `group "inner" {
  node "inherited" { source = "../module" }
  node "override" {
    source = "../module"
    backend_config = { bucket = "node-bucket" }
    backend_address = { s3_key_prefix = "node" }
  }
  node "filename" {
    source = "../module"
    backend_address = { s3_key_name = "state.json" }
  }
  node "unprefixed" {
    source = "../module"
    backend_address = { s3_key_prefix = "" }
  }
  node "disabled" {
    source = "../disabled-module"
    backend_address = {}
  }
}`)
	writeFixtureFile(t, filepath.Join(root, "outer", "group.hcl"), `group "outer" {
  use "inner" {
    as = "default"
    source = "../inner"
  }
  use "inner" {
    as = "override"
    source = "../inner"
    backend_config = { bucket = "inner-bucket" }
    backend_address = { s3_key_prefix = "inner" }
  }
  use "inner" {
    as = "disabled"
    source = "../inner"
    backend_config = { bucket = "disabled-bucket" }
    backend_address = {}
  }
}`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `use "outer" {
  as = "prod"
  source = "./outer"
  backend_config = { bucket = "outer-bucket", region = "ap-northeast-2" }
  backend_address = { s3_key_prefix = "outer", s3_key_name = "custom.tfstate" }
}`)
	g := parseAndBuild(t, root)
	for name, want := range map[string]string{
		"prod.default.inherited":   "outer/prod.default.inherited/custom.tfstate",
		"prod.override.inherited":  "inner/prod.override.inherited/custom.tfstate",
		"prod.default.override":    "node/prod.default.override/custom.tfstate",
		"prod.override.override":   "node/prod.override.override/custom.tfstate",
		"prod.disabled.override":   "node/prod.disabled.override/terraform.tfstate",
		"prod.default.filename":    "outer/prod.default.filename/state.json",
		"prod.override.filename":   "inner/prod.override.filename/state.json",
		"prod.disabled.filename":   "prod.disabled.filename/state.json",
		"prod.default.unprefixed":  "prod.default.unprefixed/custom.tfstate",
		"prod.disabled.unprefixed": "prod.disabled.unprefixed/terraform.tfstate",
	} {
		if got := g.Nodes[name].BackendConfig["key"]; got != want {
			t.Fatalf("node.%s key = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"prod.default.disabled", "prod.override.disabled", "prod.disabled.inherited", "prod.disabled.disabled"} {
		if key, exists := g.Nodes[name].BackendConfig["key"]; exists {
			t.Fatalf("node.%s key = %q, want no generated key", name, key)
		}
	}
	for name, bucket := range map[string]string{"prod.default.inherited": "outer-bucket", "prod.override.inherited": "inner-bucket", "prod.override.override": "node-bucket"} {
		cfg := g.Nodes[name].BackendConfig
		if cfg["bucket"] != bucket || cfg["region"] != "ap-northeast-2" {
			t.Fatalf("node.%s config = %v, want bucket %q and inherited region", name, cfg, bucket)
		}
	}
}

func TestBuild_BackendAddressDoesNotRepairInheritedCollision(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "module"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "group", "group.hcl"), `group "service" {
  node "a" {
    source = "../module"
    backend_config = { profile = "first" }
  }
  node "b" {
    source = "../module"
    backend_config = { profile = "second" }
    backend_address = { s3_key_prefix = "override" }
  }
}`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `use "service" {
  as = "checkout"
  source = "./group"
  backend_config = { bucket = "shared", key = "shared.tfstate", region = "ap-northeast-2" }
  backend_address = { s3_key_prefix = "prod" }
}`)
	if err := parseAndBuildErr(t, root); err == nil || !strings.Contains(err.Error(), "same s3 state") {
		t.Fatalf("error = %v, want inherited state collision despite different profiles", err)
	}
}

func TestValidate_BackendAddressGeneratedExplicitCollision(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "generated"), s3Backend)
	writeBackendModule(t, filepath.Join(root, "explicit"), `terraform {
  backend "s3" {
    bucket = "shared"
    key = "prod/generated/terraform.tfstate"
    region = "ap-northeast-2"
  }
}`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "generated" {
  source = "./generated"
  backend_config = { bucket = "shared", region = "ap-northeast-2" }
  backend_address = { s3_key_prefix = "prod" }
}
node "explicit" { source = "./explicit" }
`)
	if problems := Validate(parseAndBuild(t, root)); !hasErrorContaining(problems, "same s3 state") {
		t.Fatalf("problems = %v, want generated/explicit state collision", problems)
	}
}

func TestValidate_BackendAddressGeneratedKeysStayDistinct(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "module"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "a" {
  source = "./module"
  backend_config = { bucket = "shared", region = "ap-northeast-2" }
  backend_address = { s3_key_prefix = "prod", s3_key_name = "state.json" }
}
node "ab" {
  source = "./module"
  backend_config = { bucket = "shared", region = "ap-northeast-2" }
  backend_address = { s3_key_prefix = "prod/", s3_key_name = "state.json" }
}
`)
	g := parseAndBuild(t, root)
	for _, name := range []string{"a", "ab"} {
		if got, want := g.Nodes[name].BackendConfig["key"], "prod/"+name+"/state.json"; got != want {
			t.Fatalf("node.%s key = %q, want %q", name, got, want)
		}
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("problems = %v, want no generated/generated collision", problems)
	}
}

func TestBuild_BackendAddressPreservesModuleKeys(t *testing.T) {
	for _, key := range []string{`"legacy.tfstate"`, `var.state_key`, `""`} {
		t.Run(key, func(t *testing.T) {
			root := t.TempDir()
			writeBackendModule(t, filepath.Join(root, "module"), fmt.Sprintf("terraform {\n backend \"s3\" { key = %s }\n}\n", key))
			writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "app" {
  source = "./module"
  backend_address = { s3_key_prefix = "prod" }
}`)
			if got, exists := parseAndBuild(t, root).Nodes["app"].BackendConfig["key"]; exists {
				t.Fatalf("init override = %q, want module key %s preserved", got, key)
			}
		})
	}
}

func TestBuild_BackendAddressPreservesEmptyExplicitKey(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "module"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "app" {
  source = "./module"
  backend_config = { key = "" }
  backend_address = { s3_key_prefix = "prod" }
}`)
	if got, exists := parseAndBuild(t, root).Nodes["app"].BackendConfig["key"]; !exists || got != "" {
		t.Fatalf("key = %q, exists = %v, want unchanged empty explicit key", got, exists)
	}
}

func TestBuild_BackendAddressUnknownNonAddressDoesNotBlockGeneration(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "module"), `terraform {
  backend "s3" {
    endpoints = { s3 = "https://example.invalid" }
  }
}`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "app" {
  source = "./module"
  backend_address = { s3_key_prefix = "prod" }
}`)
	if got := parseAndBuild(t, root).Nodes["app"].BackendConfig["key"]; got != "prod/app/terraform.tfstate" {
		t.Fatalf("key = %q, want %q", got, "prod/app/terraform.tfstate")
	}
}

func TestBuild_BackendAddressOtherBackendsUnchanged(t *testing.T) {
	for _, backend := range []string{"local", "gcs", "azurerm", "http", "remote", "cloud", ""} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			body := ""
			if backend == "cloud" {
				body = cloudBackend
			} else if backend != "" {
				body = fmt.Sprintf("terraform {\n backend %q {}\n}\n", backend)
			}
			writeBackendModule(t, filepath.Join(root, "module"), body)
			writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "app" {
  source = "./module"
  backend_address = { s3_key_prefix = "prod" }
}`)
			cfg := parseAndBuild(t, root).Nodes["app"].BackendConfig
			if backend == "local" {
				if want := filepath.Join(root, ".terragraph", "state", "app.tfstate"); len(cfg) != 1 || cfg["path"] != want {
					t.Fatalf("config = %v, want unchanged local path %q", cfg, want)
				}
			} else if len(cfg) != 0 {
				t.Fatalf("config = %v, want no generated address for %q", cfg, backend)
			}
		})
	}
}

func TestBuild_BackendAddressRespectsEffectiveModuleOverride(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "module"), `terraform {
  backend "s3" { key = var.discarded }
}`)
	writeFixtureFile(t, filepath.Join(root, "module", "override.tf"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "app" {
  source = "./module"
  backend_address = { s3_key_prefix = "prod" }
}`)
	if got := parseAndBuild(t, root).Nodes["app"].BackendConfig["key"]; got != "prod/app/terraform.tfstate" {
		t.Fatalf("key = %q, want generation after the override removes the old key", got)
	}
	writeFixtureFile(t, filepath.Join(root, "module", "override.tf"), `terraform {
  backend "s3" { key = var.existing }
}`)
	if got, exists := parseAndBuild(t, root).Nodes["app"].BackendConfig["key"]; exists {
		t.Fatalf("init key override = %q, want effective module expression preserved", got)
	}
}

func TestBuild_BackendAddressPreservesJSONModuleKey(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "module", "main.tf.json"), `{"terraform":{"backend":{"s3":{"key":"${var.state_key}"}}}}`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "app" {
  source = "./module"
  backend_address = { s3_key_prefix = "prod" }
}`)
	if got, exists := parseAndBuild(t, root).Nodes["app"].BackendConfig["key"]; exists {
		t.Fatalf("init key override = %q, want JSON module expression preserved", got)
	}
}

func TestBuild_BackendAddressUnreadableAttributesRequireExplicitKey(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "module"), `terraform {
  backend "s3" {
    unexpected {}
  }
}`)
	body := `node "app" {
  source = "./module"
  backend_address = { s3_key_prefix = "prod" }
}`
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), body)
	if err := parseAndBuildErr(t, root); err == nil || !strings.Contains(err.Error(), "cannot establish whether") || !strings.Contains(err.Error(), "set backend_config.key") {
		t.Fatalf("error = %v, want unresolved key presence with remedy", err)
	}
	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	g, err := BuildObservation(bp, root)
	if err != nil || g == nil || g.Nodes["app"].ObservationError == nil {
		t.Fatalf("observation graph = %v, error = %v, want per-node observation failure", g, err)
	}
	if key, exists := g.Nodes["app"].BackendConfig["key"]; exists {
		t.Fatalf("observed key = %q, want no guessed address", key)
	}
}

func TestBuild_BackendAddressPrefixIsLiteral(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "module"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "app" {
  source = "./module"
  backend_address = { s3_key_prefix = "prod//{literal}", s3_key_name = "state.json" }
}`)
	if got := parseAndBuild(t, root).Nodes["app"].BackendConfig["key"]; got != "prod//{literal}/app/state.json" {
		t.Fatalf("key = %q, want literal prefix without interpolation or path cleaning", got)
	}
}

func TestBuild_BackendAddressIsOptIn(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "module"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "app" { source = "./module" }`)
	if cfg := parseAndBuild(t, root).Nodes["app"].BackendConfig; len(cfg) != 0 {
		t.Fatalf("config = %v, want no generated key without a rule", cfg)
	}
}
