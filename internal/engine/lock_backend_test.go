package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func TestValidate_LockRequiresRemoteBackend(t *testing.T) {
	root := t.TempDir()
	mod := filepath.Join(root, "stacks", "vpc")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "main.tf"), []byte(`
terraform {
  backend "local" {}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	bp := filepath.Join(root, "blueprint.hcl")
	if err := os.WriteFile(bp, []byte(`
lock {
  s3 {
    bucket = "b"
    key    = "k"
    region = "r"
  }
}
node "vpc" {
  source = "./stacks/vpc"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := Load(bp, exec.Terraform, os.Stdout, os.Stderr)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var found bool
	for _, p := range e.Validate() {
		if p.IsError() && strings.Contains(p.Message, "lock:") && strings.Contains(p.Message, "remote") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected lock/remote backend error, got %v", e.Validate())
	}
}

func TestValidate_LockWithS3BackendOk(t *testing.T) {
	root := t.TempDir()
	mod := filepath.Join(root, "stacks", "vpc")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "main.tf"), []byte(`
terraform {
  backend "s3" {}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	bp := filepath.Join(root, "blueprint.hcl")
	if err := os.WriteFile(bp, []byte(`
lock {
  s3 {
    bucket = "b"
    key    = "k"
    region = "r"
  }
}
node "vpc" {
  source = "./stacks/vpc"
  backend_config = {
    bucket = "state"
    key    = "vpc"
    region = "r"
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := Load(bp, exec.Terraform, os.Stdout, os.Stderr)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, p := range e.Validate() {
		if p.IsError() && strings.Contains(p.Message, "lock:") {
			t.Fatalf("unexpected lock error: %v", p.Message)
		}
	}
}
