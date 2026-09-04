package graph

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

func contractedEdge(producerType, consumerType string, producerNullable, consumerNullable, producerSensitive, consumerSensitive bool) blueprint.Edge {
	edge := dataEdge("a", "id", "b", "id")
	edge.Contract = &blueprint.EdgeContract{
		Producer: blueprint.ContractSide{Type: producerType, Nullable: producerNullable, Sensitive: producerSensitive},
		Consumer: blueprint.ContractSide{Type: consumerType, Nullable: consumerNullable, Sensitive: consumerSensitive},
	}
	return edge
}

func contractGraph(edge blueprint.Edge) *Graph {
	g := newGraph([]string{"a", "b"}, []blueprint.Edge{edge})
	g.Nodes["a"].Schema.Outputs["id"] = true
	g.Nodes["a"].Schema.OutputDetails = map[string]module.Output{"id": {Name: "id"}}
	g.Nodes["b"].Schema.Variables["id"] = module.Variable{Name: "id", Type: "string"}
	return g
}

func contractMessages(g *Graph) string {
	problems := Validate(g)
	messages := make([]string, len(problems))
	for i, problem := range problems {
		messages[i] = problem.Message
	}
	return strings.Join(messages, "\n")
}

func TestValidate_CompatibleEdgeContract(t *testing.T) {
	g := contractGraph(contractedEdge("string", "string", false, false, false, false))
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

func TestValidate_EdgeContractRejectsUnsafeTypeConversion(t *testing.T) {
	g := contractGraph(contractedEdge("string", "number", false, false, false, false))
	if got := contractMessages(g); !strings.Contains(got, "types are not safely convertible") {
		t.Fatalf("problems = %q, want type incompatibility", got)
	}
}

func TestValidate_EdgeContractRejectsNullableProducer(t *testing.T) {
	g := contractGraph(contractedEdge("string", "string", true, false, false, false))
	if got := contractMessages(g); !strings.Contains(got, "may be null") {
		t.Fatalf("problems = %q, want nullability incompatibility", got)
	}
}

func TestValidate_EdgeContractRejectsSensitiveProducer(t *testing.T) {
	g := contractGraph(contractedEdge("string", "string", false, false, true, false))
	if got := contractMessages(g); !strings.Contains(got, "does not accept sensitive") {
		t.Fatalf("problems = %q, want sensitivity incompatibility", got)
	}
}

func TestValidate_EdgeContractChecksConsumerModuleType(t *testing.T) {
	g := contractGraph(contractedEdge("string", "string", false, false, false, false))
	g.Nodes["b"].Schema.Variables["id"] = module.Variable{Name: "id", Type: "number"}
	if got := contractMessages(g); !strings.Contains(got, "consumer contract type string") || !strings.Contains(got, "module input type number") {
		t.Fatalf("problems = %q, want consumer/module type mismatch", got)
	}
}

func TestValidate_EdgeContractChecksProducerModuleType(t *testing.T) {
	g := contractGraph(contractedEdge("string", "string", false, false, false, false))
	g.Nodes["a"].Schema.OutputDetails["id"] = module.Output{Name: "id", Type: "object({ id = string })"}
	if got := contractMessages(g); !strings.Contains(got, "producer contract type string") || !strings.Contains(got, "module output type object({ id = string })") {
		t.Fatalf("problems = %q, want producer/module type mismatch", got)
	}
}

func TestValidate_EdgeContractChecksProducerModuleSensitivity(t *testing.T) {
	g := contractGraph(contractedEdge("string", "string", false, false, false, false))
	g.Nodes["a"].Schema.OutputDetails["id"] = module.Output{Name: "id", Sensitive: true}
	if got := contractMessages(g); !strings.Contains(got, "module output is sensitive") {
		t.Fatalf("problems = %q, want producer/module sensitivity mismatch", got)
	}
}

func TestValidate_EdgeContractAllowsSensitiveValueIntoOrdinaryModuleVariable(t *testing.T) {
	g := contractGraph(contractedEdge("string", "string", false, false, true, true))
	g.Nodes["a"].Schema.OutputDetails["id"] = module.Output{Name: "id", Sensitive: true}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("problems = %v, want ordinary variable to accept a sensitive value", problems)
	}
}

func TestValidate_UncontractedEdgeKeepsLegacyBehavior(t *testing.T) {
	g := contractGraph(dataEdge("a", "id", "b", "id"))
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

func TestBuild_GroupExpansionPreservesEdgeContract(t *testing.T) {
	root, bpPath := setupGroupFixture(t)
	writeFixtureFile(t, filepath.Join(root, "groups/g/group.hcl"), `
group "g" {
  node "a" { source = "../../modules/a" }
  node "b" { source = "../../modules/b" }

  export {
    input "x" { to = [node.a.input.x, node.b.input.z] }
    output "y" { from = node.a.output.y }
  }
}
`)
	bp, err := blueprint.ParseFile(bpPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	bp.Edges[0].Contract = contractedEdge("string", "string", false, false, false, false).Contract

	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	found := map[string]bool{}
	for _, edge := range g.Edges {
		if edge.From.Node == "vpc" {
			found[edge.To.Node] = true
			if edge.Contract == nil || edge.Contract.Producer.Type != "string" || edge.Contract.Consumer.Type != "string" {
				t.Fatalf("expanded edge lost contract: %+v", edge)
			}
		}
	}
	if !found["inst.a"] || !found["inst.b"] || len(found) != 2 {
		t.Fatalf("contracted edge did not fan out to both leaves: %+v", g.Edges)
	}
}
