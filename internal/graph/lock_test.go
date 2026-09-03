package graph

import (
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

const graphLockS3 = `
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
}
`

func TestValidate_LockWithLocalBackendIsError(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), localBackend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), graphLockS3+`
node "vpc" { source = "./stacks/vpc" }
`)

	problems := validateLockFixture(t, root)
	if !hasErrorContaining(problems, "remote lock") || !hasErrorContaining(problems, "local") {
		t.Fatalf("expected remote lock + local error, got %v", problems)
	}
}

func TestValidate_LockWithNoBackendBlockIsError(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), "")
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), graphLockS3+`
node "vpc" { source = "./stacks/vpc" }
`)

	problems := validateLockFixture(t, root)
	if !hasErrorContaining(problems, "no backend block") {
		t.Fatalf("expected no backend block error, got %v", problems)
	}
}

func TestValidate_LockWithAllRemoteBackendsOK(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), s3Backend)
	writeBackendModule(t, filepath.Join(root, "stacks/eks"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), graphLockS3+`
node "vpc" {
  source         = "./stacks/vpc"
  backend_config = { key = "prod/vpc" }
}
node "eks" {
  source         = "./stacks/eks"
  backend_config = { key = "prod/eks" }
}
`)

	if problems := validateLockFixture(t, root); len(problems) != 0 {
		t.Fatalf("expected no problems for lock + remote backends, got %v", problems)
	}
}

func TestValidate_NoLockWithLocalOK(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), localBackend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./stacks/vpc" }
`)

	if problems := validateLockFixture(t, root); len(problems) != 0 {
		t.Fatalf("expected no problems without lock + local backend, got %v", problems)
	}
}

func TestValidate_LockKeyEqualsNodeStateKeyIsError(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "prod/vpc"
    region = "ap-northeast-2"
  }
}
node "vpc" {
  source         = "./stacks/vpc"
  backend_config = { key = "prod/vpc" }
}
`)

	problems := validateLockFixture(t, root)
	if !hasErrorContaining(problems, "must not be a node's state key") {
		t.Fatalf("expected key collision error, got %v", problems)
	}
}

func TestValidate_LockKeyDistinctFromNodeKeysOK(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "stacks/vpc"), s3Backend)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), graphLockS3+`
node "vpc" {
  source         = "./stacks/vpc"
  backend_config = { key = "prod/vpc" }
}
`)

	if problems := validateLockFixture(t, root); len(problems) != 0 {
		t.Fatalf("expected no problems when lock key is distinct, got %v", problems)
	}
}

func validateLockFixture(t *testing.T, root string) []Problem {
	t.Helper()
	bp, dir, err := blueprint.LoadPath(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	g, err := Build(bp, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return Validate(g)
}
