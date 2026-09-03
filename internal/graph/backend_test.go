package graph

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func writeBackendModule(t *testing.T, dir, terraformBody string) {
	t.Helper()
	src := terraformBody
	if src != "" {
		src += "\n"
	}
	src += "output \"id\" { value = \"x\" }\n"
	writeFixtureFile(t, filepath.Join(dir, "main.tf"), src)
}

const localBackend = `
terraform {
  backend "local" {}
}
`

const s3Backend = `
terraform {
  backend "s3" {}
}
`

const cloudBackend = `
terraform {
  cloud {}
}
`

func TestBuild_FillsLocalBackendPath(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), localBackend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./stacks/vpc" }
`)

	bp, dir, err := blueprint.LoadPath(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := filepath.Join(dir, ".terragraph", "state", "vpc.tfstate")
	got := g.Nodes["vpc"].BackendConfig["path"]
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected an absolute path, got %q", got)
	}
}

func TestBuild_ExplicitPathWins(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), localBackend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" {
  source         = "./stacks/vpc"
  backend_config = { path = "custom.tfstate" }
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
	if got := g.Nodes["vpc"].BackendConfig["path"]; got != "custom.tfstate" {
		t.Fatalf("path = %q, want %q", got, "custom.tfstate")
	}
}

func TestBuild_FillSkipsNonLocal(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./stacks/vpc" }
`)

	bp, dir, err := blueprint.LoadPath(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := g.Nodes["vpc"].BackendConfig["path"]; ok {
		t.Fatalf("did not expect a filled path on s3, got %+v", g.Nodes["vpc"].BackendConfig)
	}
}

func TestBuild_FillSkipsNoBackendBlock(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), "")
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./stacks/vpc" }
`)

	bp, dir, err := blueprint.LoadPath(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Nodes["vpc"].BackendConfig) != 0 {
		t.Fatalf("expected empty backend_config, got %+v", g.Nodes["vpc"].BackendConfig)
	}
}

func TestBuild_FillGroupLeafUsesOuterRoot(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "modules/cluster"), localBackend)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "cluster" { source = "../../modules/cluster" }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "g" {
  as     = "checkout"
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

	got := g.Nodes["checkout.cluster"].BackendConfig["path"]
	want := filepath.Join(dir, ".terragraph", "state", "checkout.cluster.tfstate")
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if strings.Contains(got, filepath.Join("groups", "g")) {
		t.Fatalf("fill used the group directory, got %q", got)
	}
}

func TestBuild_UseBackendConfigMerges(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "modules/a"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "inherited" { source = "../../modules/a" }
  node "explicit" {
    source         = "../../modules/a"
    backend_config = { path = "from-node" }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "g" {
  as     = "inst"
  source = "./groups/g"
  backend_config = {
    bucket = "acct"
    path   = "from-use"
  }
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

	inherited := g.Nodes["inst.inherited"].BackendConfig
	if inherited["bucket"] != "acct" || inherited["path"] != "from-use" {
		t.Fatalf("expected use backend_config to cascade, got %+v", inherited)
	}
	explicit := g.Nodes["inst.explicit"].BackendConfig
	if explicit["path"] != "from-node" {
		t.Fatalf("expected leaf path to win, got %+v", explicit)
	}
	if explicit["bucket"] != "acct" {
		t.Fatalf("expected use bucket to remain when the leaf only set path, got %+v", explicit)
	}
}

func TestValidate_BackendConfigWithoutBackendBlock(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), "")
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" {
  source         = "./stacks/vpc"
  backend_config = { path = "x" }
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
	problems := Validate(g)
	if !hasErrorContaining(problems, "backend_config is set") || !hasErrorContaining(problems, "no backend block") {
		t.Fatalf("expected Error A, got %v", problems)
	}
}

func TestValidate_BackendConfigOnLocalAfterFillIsOk(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), localBackend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./stacks/vpc" }
`)

	bp, dir, err := blueprint.LoadPath(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected no problems after fill, got %v", problems)
	}
}

func TestValidate_BackendConfigOnS3IsOk(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" {
  source         = "./stacks/vpc"
  backend_config = { bucket = "b" }
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
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected no problems for s3 backend_config, got %v", problems)
	}
}

func TestValidate_BackendConfigOnCloudIsError(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), cloudBackend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" {
  source         = "./stacks/vpc"
  backend_config = { path = "x" }
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
	if g.Nodes["vpc"].Schema.Backend != "cloud" {
		t.Fatalf("Backend = %q, want cloud", g.Nodes["vpc"].Schema.Backend)
	}
	if _, ok := g.Nodes["vpc"].BackendConfig["path"]; !ok {
		t.Fatalf("fill must not drop the explicit path, got %+v", g.Nodes["vpc"].BackendConfig)
	}
	if g.Nodes["vpc"].BackendConfig["path"] != "x" {
		t.Fatalf("fill must not rewrite cloud path, got %+v", g.Nodes["vpc"].BackendConfig)
	}
	problems := Validate(g)
	if !hasErrorContaining(problems, "backend_config is set") || !hasErrorContaining(problems, "no backend block") {
		t.Fatalf("expected Error A for cloud, got %v", problems)
	}
}

func TestValidate_IdenticalBackendConfigOnSharedDir(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), localBackend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "a" {
  source         = "./stacks/vpc"
  backend_config = { path = "same.tfstate" }
}
node "b" {
  source         = "./stacks/vpc"
  backend_config = { path = "same.tfstate" }
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
	problems := Validate(g)
	if !hasErrorContaining(problems, "identical backend_config") {
		t.Fatalf("expected Error B, got %v", problems)
	}
	if !hasErrorContaining(problems, "a") || !hasErrorContaining(problems, "b") {
		t.Fatalf("expected both node names, got %v", problems)
	}
}

func TestValidate_MixedBackendConfigOnSharedDir(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), localBackend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "a" {
  source         = "./stacks/vpc"
  backend_config = { path = "shared.tfstate" }
}
node "b" {
  source         = "./stacks/vpc"
  backend_config = { path = "shared.tfstate" }
}
node "c" {
  source         = "./stacks/vpc"
  backend_config = { path = "other.tfstate" }
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
	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("expected exactly one Error B for the colliding pair, got %v", problems)
	}
	msg := problems[0].Message
	if !strings.Contains(msg, "identical backend_config") {
		t.Fatalf("expected Error B, got %q", msg)
	}
	if !strings.Contains(msg, "nodes a, b share") {
		t.Fatalf("expected colliding pair a, b in %q", msg)
	}
	if strings.Contains(msg, "nodes a, b, c") {
		t.Fatalf("c must not be named in the collision, got %q", msg)
	}
}

func TestValidate_EmptyBackendConfigOnSharedDirNoBackend(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), "")
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "a" { source = "./stacks/vpc" }
node "b" { source = "./stacks/vpc" }
`)

	bp, dir, err := blueprint.LoadPath(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !hasErrorContaining(Validate(g), "identical backend_config") {
		t.Fatalf("expected Error B for empty+empty with no backend")
	}
}

func TestValidate_EmptyBackendConfigOnSharedDirLocalIsOk(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), localBackend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "a" { source = "./stacks/vpc" }
node "b" { source = "./stacks/vpc" }
`)

	bp, dir, err := blueprint.LoadPath(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Nodes["a"].BackendConfig["path"] == g.Nodes["b"].BackendConfig["path"] {
		t.Fatalf("expected fill to isolate a and b, got %+v / %+v", g.Nodes["a"].BackendConfig, g.Nodes["b"].BackendConfig)
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected no Error B after fill, got %v", problems)
	}
}

func TestValidate_CopyPasteKeyOnSharedDir(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "a" {
  source         = "./stacks/vpc"
  backend_config = { key = "prod/vpc" }
}
node "b" {
  source         = "./stacks/vpc"
  backend_config = { key = "prod/vpc" }
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
	if !hasErrorContaining(Validate(g), "identical backend_config") {
		t.Fatalf("expected Error B for copy-paste key")
	}
}

func TestValidate_DistinctBackendConfigOnSharedDir(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), localBackend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "a" {
  source         = "./stacks/vpc"
  backend_config = { path = "a" }
}
node "b" {
  source         = "./stacks/vpc"
  backend_config = { path = "b" }
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
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected distinct maps to be valid, got %v", problems)
	}
}

func TestValidate_UniqueDirEmptyConfigIsOk(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), "")
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./stacks/vpc" }
`)

	bp, dir, err := blueprint.LoadPath(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected unique Dir with no backend to be valid, got %v", problems)
	}
}

func TestValidate_GroupUsedTwiceWithLocal(t *testing.T) {
	root, bpPath := setupSameGroupTwiceFixture(t)
	bp, err := blueprint.ParseFile(bpPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if g.Nodes["first.a"].BackendConfig["path"] == g.Nodes["second.a"].BackendConfig["path"] {
		t.Fatalf("expected distinct paths for two instances, got %+v", g.Nodes["first.a"].BackendConfig)
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("expected clean Validate, got %v", problems)
	}
}

func TestValidate_GroupUsedTwiceWithoutBackend(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "modules/a/main.tf"), `output "id" { value = "x" }`)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "g" {
  as     = "first"
  source = "./groups/g"
}
use "g" {
  as     = "second"
  source = "./groups/g"
}
`)

	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !hasErrorContaining(Validate(g), "identical backend_config") {
		t.Fatalf("expected Error B when the same group is used twice with no backend")
	}
}

func hasErrorContaining(problems []Problem, substr string) bool {
	for _, p := range problems {
		if p.IsError() && strings.Contains(p.Message, substr) {
			return true
		}
	}
	return false
}
