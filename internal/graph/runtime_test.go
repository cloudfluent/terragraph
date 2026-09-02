package graph

import (
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// setupRuntimeFixture builds, under a temp dir, a blueprint exercising every layer of runtime resolution:
//
//	modules/a:              no variables, no outputs (schema content doesn't matter for these tests)
//	groups/g/group.hcl:     its own `runtime "grouplocal" { default = true }` (deliberately never consulted:
//	                        see blueprint.Runtime.Default), node "explicit" (names it directly), node
//	                        "inherited" (names none, so it must fall back to whatever the `use` that
//	                        instantiated this group passed down)
//	blueprint.hcl:          `runtime "tofu"` and `runtime "legacy"`; node "picks_tofu" (explicit), node
//	                        "picks_nothing" (explicit reference at all, no use, no default -> nil), and a
//	                        `use "g" { runtime = runtime.legacy }` instantiating the group above
//
// returning the root dir and the path to blueprint.hcl.
func setupRuntimeFixture(t *testing.T) (root, blueprintPath string) {
	t.Helper()
	root = t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/a/main.tf"), `
output "id" { value = "x" }
`)

	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
runtime "grouplocal" {
  binary  = "grouplocal-binary"
  default = true
}

group "g" {
  node "explicit" {
    source  = "../../modules/a"
    runtime = runtime.grouplocal
  }
  node "inherited" {
    source = "../../modules/a"
  }
}
`)

	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
runtime "tofu" {
  binary  = "tofu"
  version = ">= 1.8.0"
}
runtime "legacy" {
  binary = "/opt/terraform_1.5.7"
}

node "picks_tofu" {
  source  = "./modules/a"
  runtime = runtime.tofu
}
node "picks_nothing" {
  source = "./modules/a"
}

use "g" {
  as      = "inst"
  source  = "./groups/g"
  runtime = runtime.legacy
}
`)

	return root, filepath.Join(root, "blueprint.hcl")
}

func TestBuild_RuntimeExplicitOnNode(t *testing.T) {
	_, path := setupRuntimeFixture(t)
	bp, dir, err := blueprint.LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	rt := g.Nodes["picks_tofu"].Runtime
	if rt == nil || rt.Binary != "tofu" || rt.Version != ">= 1.8.0" {
		t.Fatalf("unexpected runtime for picks_tofu: %+v", rt)
	}
}

func TestBuild_RuntimeUnsetAndNoAmbientIsNil(t *testing.T) {
	_, path := setupRuntimeFixture(t)
	bp, dir, err := blueprint.LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if rt := g.Nodes["picks_nothing"].Runtime; rt != nil {
		t.Fatalf("expected nil runtime for picks_nothing (no explicit runtime, no ambient), got %+v", rt)
	}
}

func TestBuild_RuntimeUseOverrideCascadesToInheritingInternalNode(t *testing.T) {
	_, path := setupRuntimeFixture(t)
	bp, dir, err := blueprint.LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	rt := g.Nodes["inst.inherited"].Runtime
	if rt == nil || rt.Binary != "/opt/terraform_1.5.7" {
		t.Fatalf("expected inst.inherited to inherit the use block's runtime override, got %+v", rt)
	}
}

func TestBuild_RuntimeNodeOwnChoiceBeatsUseOverride(t *testing.T) {
	_, path := setupRuntimeFixture(t)
	bp, dir, err := blueprint.LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	rt := g.Nodes["inst.explicit"].Runtime
	if rt == nil || rt.Binary != "grouplocal-binary" {
		t.Fatalf("expected inst.explicit's own runtime.grouplocal to win over the use override, got %+v", rt)
	}
}

func TestBuild_RuntimeGroupsOwnDefaultIsNeverConsulted(t *testing.T) {
	// Rebuild the same fixture but without the use block's own runtime override, so "inherited" has
	// nothing cascading down to it. If the group's own `default = true` runtime were ever consulted,
	// this node would resolve to "grouplocal-binary"; per blueprint.Runtime.Default, it must not.
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "modules/a/main.tf"), `output "id" { value = "x" }`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
runtime "grouplocal" {
  binary  = "grouplocal-binary"
  default = true
}

group "g" {
  node "inherited" {
    source = "../../modules/a"
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "g" {
  as     = "inst"
  source = "./groups/g"
}
`)

	bp, dir, err := blueprint.LoadPath(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if rt := g.Nodes["inst.inherited"].Runtime; rt != nil {
		t.Fatalf("expected the group's own default runtime to never apply automatically, got %+v", rt)
	}
}
