package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDirTemp writes files (keyed by filename relative to a fresh temp dir) and returns the temp dir's root.
func writeDirTemp(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
	return dir
}

func TestParseDir_MergesMultipleFilesAndCrossFileEdge(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{
		"nodes.hcl": `
node "vpc" { source = "./stacks/vpc" }
node "eks" { source = "./stacks/eks" }
`,
		"edges.hcl": `
edge {
  from = node.vpc.output.vpc_id
  to   = node.eks.input.vpc_id
}
`,
	})

	bp, err := ParseDir(dir)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(bp.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(bp.Nodes))
	}
	if len(bp.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(bp.Edges))
	}
	e := bp.Edges[0]
	if e.From.Node != "vpc" || e.To.Node != "eks" {
		t.Fatalf("unexpected edge: %+v", e)
	}
}

func TestParseDir_DuplicateNodeAcrossFiles(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{
		"a.hcl": `node "x" { source = "./a" }`,
		"b.hcl": `node "x" { source = "./b" }`,
	})

	if _, err := ParseDir(dir); err == nil {
		t.Fatalf("expected an error for a node name duplicated across files")
	}
}

func TestParseDir_DuplicateGroupAcrossFiles(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{
		"a.hcl": `group "g" { node "a" { source = "./a" } }`,
		"b.hcl": `group "g" { node "b" { source = "./b" } }`,
	})

	if _, err := ParseDir(dir); err == nil {
		t.Fatalf("expected an error for a group name duplicated across files")
	}
}

func TestParseDir_DuplicateVendorBlockAcrossFiles(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{
		"a.hcl": `vendor { directory = "vendor_a" }`,
		"b.hcl": `vendor { directory = "vendor_b" }`,
	})

	if _, err := ParseDir(dir); err == nil {
		t.Fatalf("expected an error for a vendor block duplicated across files")
	}
}

func TestParseDir_DuplicateLockBlockAcrossFiles(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{
		"a.hcl": `
lock {
  s3 {
    bucket = "a"
    key    = "a.lock"
    region = "ap-northeast-2"
  }
}
`,
		"b.hcl": `
lock {
  s3 {
    bucket = "b"
    key    = "b.lock"
    region = "us-east-1"
  }
}
`,
	})

	if _, err := ParseDir(dir); err == nil {
		t.Fatalf("expected an error for a lock block duplicated across files")
	}
}

func TestParseDir_IgnoresNonHCLFilesAndSubdirectories(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{
		"nodes.hcl":        `node "a" { source = "./a" }`,
		"README.md":        "not hcl",
		"nested/other.hcl": `node "b" { source = "./b" }`,
		"terragraph.notes": "also not hcl",
	})

	bp, err := ParseDir(dir)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(bp.Nodes) != 1 || bp.Nodes[0].Name != "a" {
		t.Fatalf("expected only the top-level node from nodes.hcl, got %+v", bp.Nodes)
	}
}

func TestParseDir_EmptyDirectoryIsEmptyBlueprint(t *testing.T) {
	dir := t.TempDir()

	bp, err := ParseDir(dir)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(bp.Nodes) != 0 || len(bp.Edges) != 0 || len(bp.Groups) != 0 || len(bp.Uses) != 0 {
		t.Fatalf("expected an empty Blueprint, got %+v", bp)
	}
}

func TestLoadPath_File(t *testing.T) {
	path := writeTemp(t, `node "a" { source = "./a" }`)

	bp, baseDir, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if len(bp.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(bp.Nodes))
	}
	if want := filepath.Dir(path); baseDir != want {
		t.Fatalf("baseDir = %q, want %q", baseDir, want)
	}
}

func TestLoadPath_Directory(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{
		"nodes.hcl": `node "a" { source = "./a" }`,
	})

	bp, baseDir, err := LoadPath(dir)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if len(bp.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(bp.Nodes))
	}
	if baseDir != dir {
		t.Fatalf("baseDir = %q, want %q", baseDir, dir)
	}
}

func TestParseDir_ExcludesTerraformLockButKeepsHiddenConfiguration(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{
		".terraform.lock.hcl": `provider "registry.terraform.io/hashicorp/null" { version = "3.2.4" }`,
		".nodes.hcl":          `node "a" { source = "./a" }`,
	})
	bp, err := ParseDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(bp.Nodes) != 1 || bp.Nodes[0].Name != "a" {
		t.Fatalf("got = %v, want node a", bp.Nodes)
	}
}

func TestParseDir_RejectsOtherToolsHCL(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{"backend.hcl": `bucket = "state"`})
	if _, err := ParseDir(dir); err == nil || !strings.Contains(err.Error(), "Unsupported argument") {
		t.Fatalf("got = %v, want unsupported argument", err)
	}
}

func TestLoadPath_RejectsDirectoryWithoutConfiguration(t *testing.T) {
	for _, contents := range []struct {
		name  string
		files map[string]string
	}{
		{name: "empty"},
		{name: "terraform_only", files: map[string]string{"main.tf": `output "id" { value = "a" }`}},
		{name: "lock_only", files: map[string]string{".terraform.lock.hcl": `provider "example" {}`}},
		{name: "nested_only", files: map[string]string{"nested/nodes.hcl": `node "a" { source = "./a" }`}},
	} {
		t.Run(contents.name, func(t *testing.T) {
			dir := writeDirTemp(t, contents.files)
			_, _, err := LoadPath(dir)
			if err == nil || !strings.Contains(err.Error(), "no configuration files") || !strings.Contains(err.Error(), "--blueprint") {
				t.Fatalf("got = %v, want missing configuration with path remedy", err)
			}
		})
	}
}

func TestLoadPath_AcceptsConfigurationWithoutExecutableNodes(t *testing.T) {
	for _, source := range []string{"", `group "empty" {}`} {
		dir := writeDirTemp(t, map[string]string{"configuration.hcl": source})
		bp, _, err := LoadPath(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(bp.Nodes) != 0 {
			t.Fatalf("got = %v, want no nodes", bp.Nodes)
		}
	}
}

func TestLoadPath_ExplicitFileIgnoresDirectoryFilter(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{
		".terraform.lock.hcl": `node "explicit" { source = "./a" }`,
		"invalid.hcl":         "not valid hcl {{{",
	})
	bp, _, err := LoadPath(filepath.Join(dir, ".terraform.lock.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bp.Nodes) != 1 || bp.Nodes[0].Name != "explicit" {
		t.Fatalf("got = %v, want explicit file's node", bp.Nodes)
	}
}
