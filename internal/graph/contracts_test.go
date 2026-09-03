package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// writeContractsFixture builds two leaf modules wired by one data edge, plus a contracts.hcl scoping both by source directory, and returns the built graph and the parsed contracts for the caller to compose.
func writeContractsFixture(t *testing.T, contractsHCL string) (*Graph, *blueprint.Contracts, string) {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "modules/vpc/main.tf"), `output "vpc_id" { value = "x" }`)
	writeFixtureFile(t, filepath.Join(root, "modules/app/main.tf"), `
variable "vpc_id" { type = string }
output "ok" { value = var.vpc_id }
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./modules/vpc" }
node "app" { source = "./modules/app" }
edge {
  from = node.vpc.output.vpc_id
  to   = node.app.input.vpc_id
}
`)
	if contractsHCL != "" {
		writeFixtureFile(t, filepath.Join(root, "contracts.hcl"), contractsHCL)
	}
	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var c *blueprint.Contracts
	if contractsHCL != "" {
		c, _, err = blueprint.ParseContracts(filepath.Join(root, "contracts.hcl"))
		if err != nil {
			t.Fatalf("ParseContracts: %v", err)
		}
	}
	return g, c, root
}

// TestGraph_ContractsNilWithoutFile proves the legacy default: no contracts.hcl means a nil Graph.Contracts, and Validate produces exactly the problems it did before contracts existed.
func TestGraph_ContractsNilWithoutFile(t *testing.T) {
	g, c, _ := writeContractsFixture(t, "")
	if c != nil {
		t.Fatal("fixture returned contracts for an empty file")
	}
	g.Contracts = c
	if g.Contracts != nil {
		t.Fatal("nil contracts must stay nil on the graph")
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("got = %v, want zero problems on an uncontracted graph", problems)
	}
}

// TestGraph_ContractsSharedBySourceReuse proves the identity rule the whole design rests on: two nodes sharing one source directory see one DirContracts record and therefore one digest — no per-node contract copies, no per-instance drift.
func TestGraph_ContractsSharedBySourceReuse(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "modules/shared/main.tf"), `output "id" { value = "x" }`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "one" { source = "./modules/shared" }
node "two" { source = "./modules/shared" }
`)
	writeFixtureFile(t, filepath.Join(root, "contracts.hcl"), `
producer "./modules/shared" {
  output "id" { type = "string" }
}
`)
	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	c, _, err := blueprint.ParseContracts(filepath.Join(root, "contracts.hcl"))
	if err != nil {
		t.Fatalf("ParseContracts: %v", err)
	}
	g.Contracts = c

	one := g.Contracts.Lookup(g.Nodes["one"].Dir)
	two := g.Contracts.Lookup(g.Nodes["two"].Dir)
	if one == nil || one != two {
		t.Fatalf("two nodes on one source must share one DirContracts pointer, got %p vs %p", one, two)
	}
	if _, statErr := os.Stat(one.Dir); statErr != nil {
		t.Fatalf("contract dir %s does not exist", one.Dir)
	}
}

// TestValidate_ContractTypeIncompatibilityIsWarning proves C003: a producer type not convertible to the consumer's requirement is reported with the code, both scopes, and warning severity — advisory in phase 1 by design (docs/contracts.md).
func TestValidate_ContractTypeIncompatibilityIsWarning(t *testing.T) {
	g, c, _ := writeContractsFixture(t, `
producer "./modules/vpc" {
  output "vpc_id" { type = "list(string)" }
}
consumer "./modules/app" {
  input "vpc_id" { type = "string" }
}
`)
	g.Contracts = c
	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("got = %v, want exactly one problem", problems)
	}
	p := problems[0]
	if !strings.Contains(p.Message, "[C003]") || !strings.Contains(p.Message, "./modules/vpc") || !strings.Contains(p.Message, "./modules/app") || p.IsError() {
		t.Fatalf("got = %+v, want C003 warning naming both scopes", p)
	}
}

// TestValidate_ContractNullableAndSensitiveViolations proves C004 and C005 on the same edge: consumer demanding non-null from a nullable producer, and a sensitive producer feeding a consumer that does not accept sensitive values.
func TestValidate_ContractNullableAndSensitiveViolations(t *testing.T) {
	g, c, _ := writeContractsFixture(t, `
producer "./modules/vpc" {
  output "vpc_id" {
    type      = "string"
    nullable  = true
    sensitive = true
  }
}
consumer "./modules/app" {
  input "vpc_id" {
    type     = "string"
    nullable = false
  }
}
`)
	g.Contracts = c
	var codes []string
	for _, p := range Validate(g) {
		for _, want := range []string{"[C004]", "[C005]"} {
			if strings.Contains(p.Message, want) {
				codes = append(codes, want)
			}
		}
	}
	if len(codes) != 2 {
		t.Fatalf("got = %v, want C004 and C005", codes)
	}
}

// TestValidate_ContractPromisesUndeclaredPorts proves C001/C002: a contract naming a port the module never declares is a warning with the remedy, checked even when no edge wires the port — a broken promise is broken unedged too.
func TestValidate_ContractPromisesUndeclaredPorts(t *testing.T) {
	g, c, _ := writeContractsFixture(t, `
producer "./modules/vpc" {
  output "ghost" { type = "string" }
}
consumer "./modules/app" {
  input "phantom" { type = "string" }
}
`)
	g.Contracts = c
	problems := Validate(g)
	joined := ""
	for _, p := range problems {
		joined += p.Message + "\n"
	}
	if !strings.Contains(joined, "[C001]") || !strings.Contains(joined, "[C002]") {
		t.Fatalf("got = %v, want C001 and C002", problems)
	}
}

// TestValidate_ContractScopeMatchesNoNode proves C006 with its remedy: a scope pointing at a directory no node uses is almost certainly a stale path after a move.
func TestValidate_ContractScopeMatchesNoNode(t *testing.T) {
	g, c, _ := writeContractsFixture(t, `
producer "./modules/moved-away" {
  output "id" { type = "string" }
}
`)
	g.Contracts = c
	problems := Validate(g)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "[C006]") || !strings.Contains(problems[0].Message, "no node") {
		t.Fatalf("got = %v, want one C006 warning", problems)
	}
}

// TestValidate_CompatibleContractsAreSilent proves the negative: exact-match types, nullable=false on both sides, sensitive accepted — zero problems, so adoption cannot punish correct contracts with noise.
func TestValidate_CompatibleContractsAreSilent(t *testing.T) {
	g, c, _ := writeContractsFixture(t, `
producer "./modules/vpc" {
  output "vpc_id" {
    type      = "string"
    nullable  = false
    sensitive = true
  }
}
consumer "./modules/app" {
  input "vpc_id" {
    type      = "string"
    nullable  = false
    sensitive = true
  }
}
`)
	g.Contracts = c
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("got = %v, want zero problems for compatible contracts", problems)
	}
}

// TestValidate_EnforceEscalatesContractWarningsToErrors proves mode is the only severity dial: same graph, same C003, warning under legacy and error under enforce.
func TestValidate_EnforceEscalatesContractWarningsToErrors(t *testing.T) {
	g, c, _ := writeContractsFixture(t, `
producer "./modules/vpc" {
  output "vpc_id" { type = "list(string)" }
}
consumer "./modules/app" {
  input "vpc_id" { type = "string" }
}
`)
	g.Contracts = c
	if problems := Validate(g); len(problems) != 1 || problems[0].IsError() {
		t.Fatalf("legacy must stay advisory: %v", problems)
	}
	g.ContractMode = "enforce"
	if problems := Validate(g); len(problems) != 1 || !problems[0].IsError() {
		t.Fatalf("enforce must escalate to error: %v", problems)
	}
}
