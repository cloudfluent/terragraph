//go:build !windows

package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func TestBuild_VendoredSubdirSymlinkEscapeIsRejected(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "vendor", "app", blueprint.VendoredSourceFilename), `{"subdir":"linked"}`)
	outside := t.TempDir()
	writeFixtureFile(t, filepath.Join(outside, "main.tf"), `output "id" { value = "outside" }`)
	if err := os.Symlink(outside, filepath.Join(root, "vendor", "app", "linked")); err != nil {
		t.Fatal(err)
	}
	_, err := Build(&blueprint.Blueprint{Nodes: []blueprint.Node{{Name: "app", Source: "git::https://example.com/repo.git"}}}, root)
	if err == nil {
		t.Fatalf("Build accepted a subdir symlink outside the package")
	}
}

func TestBuild_DanglingVendoredMetadataIsRejected(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "vendor", "app")
	writeFixtureFile(t, filepath.Join(dir, "main.tf"), `output "id" { value = "legacy" }`)
	if err := os.Symlink("missing-metadata", filepath.Join(dir, blueprint.VendoredSourceFilename)); err != nil {
		t.Fatal(err)
	}
	_, err := Build(&blueprint.Blueprint{Nodes: []blueprint.Node{{Name: "app", Source: "git::https://example.com/repo.git"}}}, root)
	if err == nil {
		t.Fatalf("Build fell back from dangling metadata to the package root")
	}
}
