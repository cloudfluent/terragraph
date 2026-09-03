package blueprint

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing temp blueprint: %v", err)
	}
	return path
}

func TestParseFile_DataEdge(t *testing.T) {
	path := writeTemp(t, `
node "vpc" {
  source = "./stacks/vpc"
}

node "eks" {
  source = "./stacks/eks"
}

edge {
  from = node.vpc.output.vpc_id
  to   = node.eks.input.vpc_id
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(bp.Nodes))
	}
	if len(bp.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(bp.Edges))
	}
	e := bp.Edges[0]
	if !e.IsDataEdge() {
		t.Fatalf("expected a data edge")
	}
	if e.From.Node != "vpc" || e.From.Kind != PortOutput || e.From.Name != "vpc_id" {
		t.Fatalf("unexpected From: %+v", e.From)
	}
	if e.To.Node != "eks" || e.To.Kind != PortInput || e.To.Name != "vpc_id" {
		t.Fatalf("unexpected To: %+v", e.To)
	}
}

func TestParseFile_ImplicitEdge(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }

edge {
  from = node.a
  to   = node.b
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	e := bp.Edges[0]
	if e.IsDataEdge() {
		t.Fatalf("expected an ordering-only edge, got a data edge")
	}
	if e.From.Node != "a" || e.From.IsPort() {
		t.Fatalf("unexpected From: %+v", e.From)
	}
	if e.To.Node != "b" || e.To.IsPort() {
		t.Fatalf("unexpected To: %+v", e.To)
	}
}

func TestParseFile_RejectsReferenceWithoutName(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
edge { from = node to = node.a }
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatal("expected an error for a reference without a node name")
	}
}

func TestParseFile_MixedEdgeRejected(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }

edge {
  from = node.a.output.x
  to   = node.b
}
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error mixing a port reference with a bare node reference")
	}
}

func TestParseFile_UnknownNodeReference(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }

edge {
  from = node.a.output.x
  to   = node.missing.input.y
}
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for an edge referencing an unknown node")
	}
}

func TestParseFile_DuplicateNode(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "a" { source = "./a2" }
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a duplicate node name")
	}
}

func TestParseFile_BackendConfig(t *testing.T) {
	path := writeTemp(t, `
node "vpc_prod" {
  source = "./stacks/vpc"
  backend_config = {
    path = ".terragraph/state/vpc_prod.tfstate"
    key  = "prod/vpc"
  }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	node, ok := bp.NodeByName("vpc_prod")
	if !ok {
		t.Fatalf("expected node vpc_prod")
	}
	if node.BackendConfig["path"] != ".terragraph/state/vpc_prod.tfstate" {
		t.Fatalf("unexpected path: %+v", node.BackendConfig)
	}
	if node.BackendConfig["key"] != "prod/vpc" {
		t.Fatalf("unexpected key: %+v", node.BackendConfig)
	}
}

func TestParseFile_NoBackendConfigIsNil(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	node, _ := bp.NodeByName("a")
	if len(node.BackendConfig) != 0 {
		t.Fatalf("expected no backend_config, got %+v", node.BackendConfig)
	}
}

func TestParseFile_UseBackendConfig(t *testing.T) {
	path := writeTemp(t, `
use "g" {
  as     = "inst"
  source = "./groups/g"
  backend_config = {
    bucket = "b"
  }
}
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Uses[0].BackendConfig["bucket"] != "b" {
		t.Fatalf("unexpected use backend_config: %+v", bp.Uses[0].BackendConfig)
	}
}

func TestParseFile_UseNoBackendConfigIsNil(t *testing.T) {
	path := writeTemp(t, `
use "g" {
  as     = "inst"
  source = "./groups/g"
}
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Uses[0].BackendConfig) != 0 {
		t.Fatalf("expected no use backend_config, got %+v", bp.Uses[0].BackendConfig)
	}
}

func TestParseFile_Vars(t *testing.T) {
	path := writeTemp(t, `
node "data-apne2-dev-vpc" {
  source = "./modules/vpc"
  vars = {
    name            = "dpl-apne2-vpc-dev"
    cidr            = "10.16.0.0/20"
    nat_enabled     = false
    az_count        = 3
    private_subnets = ["10.16.0.0/23", "10.16.2.0/23"]
    tags            = { tenant = "data-platform", stage = "dev" }
  }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	node, ok := bp.NodeByName("data-apne2-dev-vpc")
	if !ok {
		t.Fatalf("expected node data-apne2-dev-vpc")
	}

	if node.Vars["name"] != "dpl-apne2-vpc-dev" {
		t.Fatalf("unexpected name: %+v", node.Vars["name"])
	}
	if node.Vars["cidr"] != "10.16.0.0/20" {
		t.Fatalf("unexpected cidr: %+v", node.Vars["cidr"])
	}
	if node.Vars["nat_enabled"] != false {
		t.Fatalf("unexpected nat_enabled: %+v", node.Vars["nat_enabled"])
	}
	if node.Vars["az_count"] != float64(3) {
		t.Fatalf("unexpected az_count: %+v", node.Vars["az_count"])
	}
	subnets, ok := node.Vars["private_subnets"].([]any)
	if !ok || len(subnets) != 2 || subnets[0] != "10.16.0.0/23" {
		t.Fatalf("unexpected private_subnets: %+v", node.Vars["private_subnets"])
	}
	tags, ok := node.Vars["tags"].(map[string]any)
	if !ok || tags["tenant"] != "data-platform" || tags["stage"] != "dev" {
		t.Fatalf("unexpected tags: %+v", node.Vars["tags"])
	}
}

func TestParseFile_NoVarsIsNil(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	node, _ := bp.NodeByName("a")
	if len(node.Vars) != 0 {
		t.Fatalf("expected no vars, got %+v", node.Vars)
	}
}

func TestParseFile_VarsRejectsOutputReference(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" {
  source = "./b"
  vars = {
    x = node.a.output.id
  }
}
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error: vars must not reference another node's output; use an edge for that")
	}
}

func TestParseFile_VarsRejectsNonObject(t *testing.T) {
	path := writeTemp(t, `
node "a" {
  source = "./a"
  vars = ["not", "an", "object"]
}
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error: vars must be an object/map, not a list")
	}
}

func TestParseFile_EdgeInputBlocks(t *testing.T) {
	path := writeTemp(t, `
node "vpc" { source = "./stacks/vpc" }
node "eks" { source = "./stacks/eks" }

edge {
  from = node.vpc
  to   = node.eks

  input "vpc_id" {
    from = output.vpc_id
  }

  input "subnet_ids" {
    from = output.private_subnet_ids
  }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Edges) != 2 {
		t.Fatalf("expected the edge to expand into 2 data edges, got %d: %+v", len(bp.Edges), bp.Edges)
	}
	want := []Edge{
		{
			From: PortRef{Node: "vpc", Kind: PortOutput, Name: "vpc_id"},
			To:   PortRef{Node: "eks", Kind: PortInput, Name: "vpc_id"},
		},
		{
			From: PortRef{Node: "vpc", Kind: PortOutput, Name: "private_subnet_ids"},
			To:   PortRef{Node: "eks", Kind: PortInput, Name: "subnet_ids"},
		},
	}
	for i, w := range want {
		if bp.Edges[i] != w {
			t.Fatalf("edge %d: got %+v, want %+v", i, bp.Edges[i], w)
		}
		if !bp.Edges[i].IsDataEdge() {
			t.Fatalf("edge %d: expected a data edge", i)
		}
	}
}

func TestParseFile_EdgeInputBlocksOnUseInstance(t *testing.T) {
	path := writeTemp(t, `
node "vpc" { source = "./stacks/vpc" }

use "eks-service" {
  as     = "checkout"
  source = "./groups/eks-service"
}

edge {
  from = node.vpc
  to   = use.checkout

  input "vpc_id" {
    from = output.vpc_id
  }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(bp.Edges))
	}
	e := bp.Edges[0]
	if e.From.Entity != EntityNode || e.From.Node != "vpc" || e.From.Kind != PortOutput || e.From.Name != "vpc_id" {
		t.Fatalf("unexpected From: %+v", e.From)
	}
	if e.To.Entity != EntityUse || e.To.Node != "checkout" || e.To.Kind != PortInput || e.To.Name != "vpc_id" {
		t.Fatalf("unexpected To: %+v", e.To)
	}
}

func TestParseFile_EdgeInputBlocksInGroup(t *testing.T) {
	path := writeTemp(t, `
group "eks-service" {
  node "cluster"   { source = "./eks" }
  node "nodegroup" { source = "./eks-nodegroup" }

  edge {
    from = node.cluster
    to   = node.nodegroup

    input "cluster_id"   { from = output.cluster_id }
    input "cluster_name" { from = output.cluster_name }
  }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Groups) != 1 || len(bp.Groups[0].Edges) != 2 {
		t.Fatalf("expected the group's edge to expand into 2 edges, got %+v", bp.Groups)
	}
}

func TestParseFile_EdgeInputBlocksWithPortEndpointRejected(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }

edge {
  from = node.a.output.x
  to   = node.b.input.x

  input "y" { from = output.y }
}
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error: an edge naming specific ports cannot also carry input blocks")
	}
}

func TestParseFile_EdgeDuplicateInputLabelRejected(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }

edge {
  from = node.a
  to   = node.b

  input "x" { from = output.one }
  input "x" { from = output.two }
}
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a duplicate input label on one edge")
	}
}

func TestParseFile_EdgeInputRejectsAbsoluteReference(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }

edge {
  from = node.a
  to   = node.b

  input "x" { from = node.a.output.x }
}
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error: an input block's \"from\" must be relative (output.<attr>)")
	}
}

func TestParseFile_EdgeInputRejectsInputPortReference(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }

edge {
  from = node.a
  to   = node.b

  input "x" { from = input.x }
}
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error: an input block's \"from\" must reference an output")
	}
}

func TestParseFile_FromMustBeOutput(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }

edge {
  from = node.a.input.x
  to   = node.b.input.y
}
`)

	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error: \"from\" must be an output port")
	}
}
