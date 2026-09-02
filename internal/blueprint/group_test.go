package blueprint

import "testing"

func TestParseFile_GroupDefinition(t *testing.T) {
	path := writeTemp(t, `
group "eks-service" {
  node "cluster"   { source = "../../modules/eks" }
  node "nodegroup" { source = "../../modules/eks-nodegroup" }

  edge {
    from = node.cluster.output.cluster_id
    to   = node.nodegroup.input.cluster_id
  }

  export {
    input "vpc_id" {
      to = node.cluster.input.vpc_id
    }
    output "cluster_id" {
      from = node.cluster.output.cluster_id
    }
  }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(bp.Groups))
	}
	g := bp.Groups[0]
	if g.Name != "eks-service" {
		t.Fatalf("unexpected group name: %q", g.Name)
	}
	if len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Fatalf("unexpected group contents: %+v", g)
	}
	if len(g.Export.Inputs) != 1 || g.Export.Inputs[0].Name != "vpc_id" {
		t.Fatalf("unexpected export inputs: %+v", g.Export.Inputs)
	}
	if len(g.Export.Inputs[0].To) != 1 || g.Export.Inputs[0].To[0].Node != "cluster" {
		t.Fatalf("unexpected export input target: %+v", g.Export.Inputs[0].To)
	}
	if len(g.Export.Outputs) != 1 || g.Export.Outputs[0].Name != "cluster_id" {
		t.Fatalf("unexpected export outputs: %+v", g.Export.Outputs)
	}
}

func TestParseFile_ExportInputFanOut(t *testing.T) {
	path := writeTemp(t, `
group "g" {
  node "a" { source = "./a" }
  node "b" { source = "./b" }

  export {
    input "shared" {
      to = [node.a.input.x, node.b.input.x]
    }
  }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	in := bp.Groups[0].Export.Inputs[0]
	if len(in.To) != 2 {
		t.Fatalf("expected fan-out to 2 targets, got %d: %+v", len(in.To), in.To)
	}
	if in.To[0].Node != "a" || in.To[1].Node != "b" {
		t.Fatalf("unexpected fan-out targets: %+v", in.To)
	}
}

func TestParseFile_ExportOutputMustBeOutputPort(t *testing.T) {
	path := writeTemp(t, `
group "g" {
  node "a" { source = "./a" }
  export {
    output "bad" {
      from = node.a.input.x
    }
  }
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error: export output must map to an output port")
	}
}

func TestParseFile_ExportInputMustBeInputPort(t *testing.T) {
	path := writeTemp(t, `
group "g" {
  node "a" { source = "./a" }
  export {
    input "bad" {
      to = node.a.output.x
    }
  }
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error: export input must map to input port(s)")
	}
}

func TestParseFile_GroupEdgeUnknownNode(t *testing.T) {
	path := writeTemp(t, `
group "g" {
  node "a" { source = "./a" }
  edge {
    from = node.a.output.x
    to   = node.missing.input.y
  }
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for an edge referencing an unknown node inside the group")
	}
}

func TestParseFile_UseBlockAndEdges(t *testing.T) {
	path := writeTemp(t, `
node "vpc" { source = "./stacks/vpc" }

use "eks-service" {
  as     = "checkout"
  source = "../groups/eks-service"
}

edge {
  from = node.vpc.output.vpc_id
  to   = use.checkout.input.vpc_id
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Uses) != 1 {
		t.Fatalf("expected 1 use instantiation, got %d", len(bp.Uses))
	}
	u := bp.Uses[0]
	if u.GroupName != "eks-service" || u.As != "checkout" || u.Source != "../groups/eks-service" {
		t.Fatalf("unexpected use block: %+v", u)
	}

	if len(bp.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(bp.Edges))
	}
	to := bp.Edges[0].To
	if to.Entity != EntityUse || to.Node != "checkout" || to.Kind != PortInput || to.Name != "vpc_id" {
		t.Fatalf("unexpected edge target: %+v", to)
	}
}

func TestParseFile_EdgeToUnknownUseInstance(t *testing.T) {
	path := writeTemp(t, `
node "vpc" { source = "./stacks/vpc" }
edge {
  from = node.vpc.output.vpc_id
  to   = use.missing.input.vpc_id
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for an edge referencing an unknown use instance")
	}
}

func TestParseFile_NestedUseInsideGroup(t *testing.T) {
	path := writeTemp(t, `
group "outer" {
  use "inner-group" {
    as     = "inner"
    source = "../inner"
  }
  node "a" { source = "./a" }

  export {
    output "x" { from = use.inner.output.x }
  }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g := bp.Groups[0]
	if len(g.Uses) != 1 || g.Uses[0].As != "inner" {
		t.Fatalf("unexpected nested use: %+v", g.Uses)
	}
	if g.Export.Outputs[0].From.Entity != EntityUse {
		t.Fatalf("expected export output to reference the nested use instance, got %+v", g.Export.Outputs[0].From)
	}
}

func TestParseFile_DuplicateGroupName(t *testing.T) {
	path := writeTemp(t, `
group "g" { node "a" { source = "./a" } }
group "g" { node "b" { source = "./b" } }
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a duplicate group name")
	}
}

func TestParseFile_DuplicateUseInstanceName(t *testing.T) {
	path := writeTemp(t, `
use "g1" { as = "x" source = "../g1" }
use "g2" { as = "x" source = "../g2" }
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a duplicate use instance name")
	}
}
