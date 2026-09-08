package engine

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func TestValidate_CaseVariantArtifactPaths(t *testing.T) {
	root := t.TempDir()
	probe := filepath.Join(root, "case-probe")
	if err := os.WriteFile(probe, nil, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(filepath.Join(root, "CASE-PROBE"))
	insensitive := err == nil && os.SameFile(a, b)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}
	moduleDir := filepath.Join(root, "module")
	if err := os.MkdirAll(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "main.tf"), []byte(`
terraform {
  backend "local" {}
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "blueprint.hcl")
	if err := os.WriteFile(path, []byte(`
node "prod" { source = "./module" }
node "Prod" { source = "./module" }
`), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	found := false
	for _, p := range e.Validate() {
		if p.IsError() && strings.Contains(p.Message, "same managed path") {
			found = true
		}
	}
	if found != insensitive {
		t.Fatalf("got collision = %t, want %t from observed filesystem behavior", found, insensitive)
	}
	if _, err := os.Stat(filepath.Join(root, ".terragraph")); !os.IsNotExist(err) {
		t.Fatalf("validation wrote artifacts: %v", err)
	}
}
