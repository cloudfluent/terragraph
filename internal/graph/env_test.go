package graph

import (
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// setupEnvFixture builds, under a temp dir, a blueprint exercising env merging:
//
//	modules/a:              no variables/outputs
//	groups/g/group.hcl:     node "explicit" (own AWS_PROFILE, overriding whatever cascades in),
//	                        node "inherited" (sets nothing of its own)
//	blueprint.hcl:          use "g" { env = { AWS_PROFILE = "prod", AWS_REGION = "ap-northeast-2" } }
//
// returning the root dir and the path to blueprint.hcl.
func setupEnvFixture(t *testing.T) (root, blueprintPath string) {
	t.Helper()
	root = t.TempDir()

	writeFixtureFile(t, filepath.Join(root, "modules/a/main.tf"), `output "id" { value = "x" }`)

	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "explicit" {
    source = "../../modules/a"
    env = {
      AWS_PROFILE = "dev-override"
    }
  }
  node "inherited" {
    source = "../../modules/a"
  }
}
`)

	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "g" {
  as     = "inst"
  source = "./groups/g"
  env = {
    AWS_PROFILE = "prod"
    AWS_REGION  = "ap-northeast-2"
  }
}
`)

	return root, filepath.Join(root, "blueprint.hcl")
}

func TestBuild_EnvUseOverrideCascadesToInheritingInternalNode(t *testing.T) {
	_, path := setupEnvFixture(t)
	bp, dir, err := blueprint.LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	env := g.Nodes["inst.inherited"].Env
	if env["AWS_PROFILE"] != "prod" || env["AWS_REGION"] != "ap-northeast-2" {
		t.Fatalf("expected inst.inherited to receive the full cascaded env, got %+v", env)
	}
}

func TestBuild_EnvNodeOwnKeyOverridesCascadeButKeepsRest(t *testing.T) {
	_, path := setupEnvFixture(t)
	bp, dir, err := blueprint.LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	env := g.Nodes["inst.explicit"].Env
	if env["AWS_PROFILE"] != "dev-override" {
		t.Fatalf("expected the node's own AWS_PROFILE to win over the cascaded value, got %+v", env)
	}
	if env["AWS_REGION"] != "ap-northeast-2" {
		t.Fatalf("expected AWS_REGION to still be inherited from the use override (merge, not replace), got %+v", env)
	}
}

func TestBuild_EnvUnsetAndNoAmbientIsNil(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "modules/a/main.tf"), `output "id" { value = "x" }`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "plain" { source = "./modules/a" }
`)

	bp, dir, err := blueprint.LoadPath(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if env := g.Nodes["plain"].Env; env != nil {
		t.Fatalf("expected nil env for a node with no env anywhere in its chain, got %+v", env)
	}
}

func TestMergeEnv(t *testing.T) {
	base := map[string]string{"A": "1", "B": "2"}
	override := map[string]string{"B": "3", "C": "4"}

	got := mergeEnv(base, override)
	want := map[string]string{"A": "1", "B": "3", "C": "4"}
	if len(got) != len(want) {
		t.Fatalf("mergeEnv() = %+v, want %+v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("mergeEnv()[%q] = %q, want %q", k, got[k], v)
		}
	}

	// base must not be mutated by the merge.
	if base["B"] != "2" {
		t.Fatalf("mergeEnv mutated base: %+v", base)
	}

	if mergeEnv(nil, nil) != nil {
		t.Fatalf("expected mergeEnv(nil, nil) to be nil")
	}
}
