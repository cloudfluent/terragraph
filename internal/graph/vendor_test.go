package graph

import (
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func TestBuild_UnvendoredRemoteNodeReturnsClearError(t *testing.T) {
	root := t.TempDir()
	bp := &blueprint.Blueprint{
		Nodes: []blueprint.Node{
			{Name: "vpc", Source: "git::https://github.com/x/y.git?ref=v1.0.0"},
		},
	}

	_, err := Build(bp, root)
	if err == nil {
		t.Fatalf("expected an error for a remote source that hasn't been vendored")
	}
}

func TestBuild_AlreadyVendoredNodeResolvesFromVendorDir(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "vendor/vpc/outputs.tf"), `
output "vpc_id" { value = "x" }
`)

	bp := &blueprint.Blueprint{
		Nodes: []blueprint.Node{
			{Name: "vpc", Source: "git::https://github.com/x/y.git?ref=v1.0.0"},
		},
	}

	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !g.Nodes["vpc"].Schema.HasOutput("vpc_id") {
		t.Fatalf("expected the vendored module's real output to be inspected")
	}
	wantDir, _ := filepath.Abs(filepath.Join(root, "vendor/vpc"))
	if g.Nodes["vpc"].Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", g.Nodes["vpc"].Dir, wantDir)
	}
}

func TestBuild_CustomVendorDirectory(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "third_party/vpc/outputs.tf"), `
output "vpc_id" { value = "x" }
`)

	bp := &blueprint.Blueprint{
		Nodes: []blueprint.Node{
			{Name: "vpc", Source: "git::https://github.com/x/y.git?ref=v1.0.0"},
		},
		Vendor: &blueprint.VendorConfig{Directory: "third_party"},
	}

	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !g.Nodes["vpc"].Schema.HasOutput("vpc_id") {
		t.Fatalf("expected Build to look under the configured vendor directory")
	}
}
