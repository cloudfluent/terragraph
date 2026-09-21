package blueprint

import (
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

func assertLoc(t *testing.T, label string, got, want Loc) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got = %+v, want %+v", label, got, want)
	}
}

func TestParseFile_ProvenanceLocations(t *testing.T) {
	path := writeTemp(t, `
runtime "tofu" {
  binary = "tofu"
}

node "vpc" {
  source = "./stacks/vpc"
  vars = {
    cidr = "10.0.0.0/16"
    name = "main"
  }
  runtime = runtime.tofu
  approve = "all"
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

	assertLoc(t, "runtime block", bp.Runtimes[0].Loc, Loc{File: path, Line: 2, Column: 1})

	vpc, ok := bp.NodeByName("vpc")
	if !ok {
		t.Fatalf("node vpc not found")
	}
	assertLoc(t, "node block", vpc.Loc, Loc{File: path, Line: 6, Column: 1})
	assertLoc(t, "vars key cidr", vpc.VarsLocs["cidr"], Loc{File: path, Line: 9, Column: 5})
	assertLoc(t, "vars key name", vpc.VarsLocs["name"], Loc{File: path, Line: 10, Column: 5})
	assertLoc(t, "runtime attribute", vpc.RuntimeLoc, Loc{File: path, Line: 12, Column: 3})
	assertLoc(t, "approve attribute", vpc.ApproveLoc, Loc{File: path, Line: 13, Column: 3})

	eks, ok := bp.NodeByName("eks")
	if !ok {
		t.Fatalf("node eks not found")
	}
	assertLoc(t, "second node block", eks.Loc, Loc{File: path, Line: 16, Column: 1})
	if eks.VarsLocs != nil {
		t.Fatalf("node without vars: VarsLocs = %+v, want nil", eks.VarsLocs)
	}
	assertLoc(t, "absent runtime attribute", eks.RuntimeLoc, Loc{})
	assertLoc(t, "absent approve attribute", eks.ApproveLoc, Loc{})

	assertLoc(t, "edge block", bp.Edges[0].Loc, Loc{File: path, Line: 20, Column: 1})
}

func TestParseFile_EdgeInputBlocksCarryTheirOwnLoc(t *testing.T) {
	path := writeTemp(t, `
node "vpc" { source = "./stacks/vpc" }
node "eks" { source = "./stacks/eks" }

edge {
  from = node.vpc
  to   = node.eks

  input "cluster_name" {
    from = output.eks_cluster_name
  }

  input "vpc_id" {
    from = output.vpc_id
  }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Edges) != 2 {
		t.Fatalf("expected 2 expanded edges, got %d", len(bp.Edges))
	}
	if bp.Edges[0].To.Name != "cluster_name" || bp.Edges[1].To.Name != "vpc_id" {
		t.Fatalf("unexpected expanded edges: %+v", bp.Edges)
	}
	// Each nested input block is its own connection's declaration, not the enclosing edge block.
	assertLoc(t, "first input block", bp.Edges[0].Loc, Loc{File: path, Line: 9, Column: 3})
	assertLoc(t, "second input block", bp.Edges[1].Loc, Loc{File: path, Line: 13, Column: 3})
}

func TestParseFile_GroupProvenanceLocations(t *testing.T) {
	path := writeTemp(t, `
group "app" {
  node "web" {
    source = "./web"
  }

  use "net" {
    as     = "net"
    source = "../net"
    vars = {
      cidr = "10.0.0.0/8"
    }
    approve = "safe"
  }

  export {
    input "cidr" {
      to = [node.web.input.cidr]
    }

    output "web_url" {
      from = node.web.output.url
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

	assertLoc(t, "group block", g.Loc, Loc{File: path, Line: 2, Column: 1})
	assertLoc(t, "nested node block", g.Nodes[0].Loc, Loc{File: path, Line: 3, Column: 3})

	u := g.Uses[0]
	assertLoc(t, "use block", u.Loc, Loc{File: path, Line: 7, Column: 3})
	assertLoc(t, "use vars key", u.VarsLocs["cidr"], Loc{File: path, Line: 11, Column: 7})
	assertLoc(t, "use approve attribute", u.ApproveLoc, Loc{File: path, Line: 13, Column: 5})
	assertLoc(t, "absent use runtime attribute", u.RuntimeLoc, Loc{})

	assertLoc(t, "export block", g.Export.Loc, Loc{File: path, Line: 16, Column: 3})
	assertLoc(t, "export input block", g.Export.Inputs[0].Loc, Loc{File: path, Line: 17, Column: 5})
	assertLoc(t, "export input to attribute", g.Export.Inputs[0].ToLoc, Loc{File: path, Line: 18, Column: 7})
	assertLoc(t, "export output block", g.Export.Outputs[0].Loc, Loc{File: path, Line: 21, Column: 5})
	assertLoc(t, "export output from attribute", g.Export.Outputs[0].FromLoc, Loc{File: path, Line: 22, Column: 7})
}

func TestParseDir_ProvenanceCarriesEachDeclarationFile(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{
		"aa-wiring.hcl": `
runtime "tofu" {
  binary = "tofu"
}

edge {
  from = node.vpc
  to   = node.eks
}
`,
		"zz-nodes.hcl": `
node "vpc" {
  source = "./stacks/vpc"
}

node "eks" {
  source = "./stacks/eks"
}
`,
	})
	wiring := filepath.Join(dir, "aa-wiring.hcl")
	nodes := filepath.Join(dir, "zz-nodes.hcl")

	bp, err := ParseDir(dir)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}

	assertLoc(t, "runtime in wiring file", bp.Runtimes[0].Loc, Loc{File: wiring, Line: 2, Column: 1})
	assertLoc(t, "edge in wiring file", bp.Edges[0].Loc, Loc{File: wiring, Line: 6, Column: 1})
	assertLoc(t, "vpc in nodes file", bp.Nodes[0].Loc, Loc{File: nodes, Line: 2, Column: 1})
	assertLoc(t, "eks in nodes file", bp.Nodes[1].Loc, Loc{File: nodes, Line: 6, Column: 1})
}

func TestParseFile_VarsFromEvalContextLeavesLocsAbsent(t *testing.T) {
	path := writeTemp(t, `
node "app" {
  source = "./app"
  vars   = common.tags
}
`)
	evaluation := &hcl.EvalContext{Variables: map[string]cty.Value{
		"common": cty.ObjectVal(map[string]cty.Value{
			"tags": cty.ObjectVal(map[string]cty.Value{
				"env":  cty.StringVal("prod"),
				"tier": cty.StringVal("web"),
			}),
		}),
	}}

	bp, err := ParseFile(path, evaluation)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	node, ok := bp.NodeByName("app")
	if !ok {
		t.Fatalf("node app not found")
	}
	if node.Vars["env"] != "prod" || node.Vars["tier"] != "web" {
		t.Fatalf("vars not evaluated from context: %+v", node.Vars)
	}
	// A whole-map expression has no per-key declaration in the file, so provenance stays absent instead of failing the parse.
	if node.VarsLocs != nil {
		t.Fatalf("VarsLocs = %+v, want nil for eval-context vars", node.VarsLocs)
	}
}
