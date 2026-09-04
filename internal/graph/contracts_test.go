package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// writeContractsFixture builds two leaf modules wired by one data edge, with contractsHCL's producer/consumer blocks appended to the blueprint file itself — the ordinary spelling since contracts became blueprint blocks — and returns the built graph (Build attaches the contracts).
func writeContractsFixture(t *testing.T, contractsHCL string) (*Graph, string) {
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
`+contractsHCL)
	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g, root
}

// TestGraph_ContractsNilWithoutBlocks proves the uncontracted default: a blueprint with no producer/consumer blocks means a nil Graph.Contracts, and Validate produces exactly the problems it did before contracts existed.
func TestGraph_ContractsNilWithoutBlocks(t *testing.T) {
	g, _ := writeContractsFixture(t, "")
	if g.Contracts != nil {
		t.Fatal("contracts attached although the blueprint declares none")
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
	if g.Contracts == nil {
		t.Fatal("Build did not attach the blueprint's own contracts")
	}

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
	g, _ := writeContractsFixture(t, `
producer "./modules/vpc" {
  output "vpc_id" { type = "list(string)" }
}
consumer "./modules/app" {
  input "vpc_id" { type = "string" }
}
`)
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
	g, _ := writeContractsFixture(t, `
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
	g, _ := writeContractsFixture(t, `
producer "./modules/vpc" {
  output "ghost" { type = "string" }
}
consumer "./modules/app" {
  input "phantom" { type = "string" }
}
`)
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
	g, _ := writeContractsFixture(t, `
producer "./modules/moved-away" {
  output "id" { type = "string" }
}
`)
	problems := Validate(g)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "[C006]") || !strings.Contains(problems[0].Message, "no node") {
		t.Fatalf("got = %v, want one C006 warning", problems)
	}
}

// TestValidate_CompatibleContractsAreSilent proves the negative: exact-match types, nullable=false on both sides, sensitive accepted — with modules that actually declare sensitive = true, so the claims are true and C008/C009 stay silent too — zero problems, so adoption cannot punish correct contracts with noise.
func TestValidate_CompatibleContractsAreSilent(t *testing.T) {
	g := writeReconcileFixture(t,
		`output "vpc_id" {
  value     = "x"
  sensitive = true
}`,
		`variable "vpc_id" {
  type      = string
  sensitive = true
}`,
		`
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
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("got = %v, want zero problems for compatible contracts", problems)
	}
}

// TestValidate_PartialPortCoverageChecksNothing proves a contract on only one side of an edge's ports stays silent: a map miss must not fabricate a zero-valued PortContract whose nil flags fire C004/C005 for a port nobody promised.
func TestValidate_PartialPortCoverageChecksNothing(t *testing.T) {
	g, _ := writeContractsFixture(t, `
consumer "./modules/app" {
  input "vpc_id" {
    type     = "string"
    nullable = false
  }
}
`)
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("got = %v, want zero problems when the producer port is uncontracted", problems)
	}
}

// TestValidate_EnforceEscalatesContractWarningsToErrors proves mode is the only severity dial: same graph, same C003, warning with no mode set and error under enforce.
func TestValidate_EnforceEscalatesContractWarningsToErrors(t *testing.T) {
	g, _ := writeContractsFixture(t, `
producer "./modules/vpc" {
  output "vpc_id" { type = "list(string)" }
}
consumer "./modules/app" {
  input "vpc_id" { type = "string" }
}
`)
	if problems := Validate(g); len(problems) != 1 || problems[0].IsError() {
		t.Fatalf("unset mode must stay advisory: %v", problems)
	}
	g.ContractMode = "enforce"
	if problems := Validate(g); len(problems) != 1 || !problems[0].IsError() {
		t.Fatalf("enforce must escalate to error: %v", problems)
	}
}

// writeGroupContractsFixture builds a group whose own group.hcl contracts its internal module with producerType, instantiates it once, and wires an expanded edge into a consumer node whose contract demands string.
func writeGroupContractsFixture(t *testing.T, groupContracts string) *Graph {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "modules/net/vpc/main.tf"), `output "cidr" { value = "10.0.0.0/8" }`)
	writeFixtureFile(t, filepath.Join(root, "modules/app/main.tf"), `
variable "cidr" { type = string }
output "ok" { value = var.cidr }
`)
	writeFixtureFile(t, filepath.Join(root, "modules/net/group.hcl"), `
group "net" {
  node "vpc" { source = "./vpc" }
  export {
    output "cidr" { from = node.vpc.output.cidr }
  }
`+groupContracts+`}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "net" {
  as     = "net"
  source = "./modules/net"
}
node "app" { source = "./modules/app" }
edge {
  from = use.net.output.cidr
  to   = node.app.input.cidr
}
consumer "./modules/app" {
  input "cidr" { type = "string" }
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
	return g
}

// TestBuild_GroupCarriesOwnContracts proves the structural win the reserved filename could never express: a group's own group.hcl contracts its internal module, Build merges those contracts into the enclosing graph keyed by the internal module's real directory, and an edge expanded from a use of that group is checked against the internal promise (C003 here) with no contract file beside the root blueprint.
func TestBuild_GroupCarriesOwnContracts(t *testing.T) {
	g := writeGroupContractsFixture(t, `
  producer "./vpc" {
    output "cidr" { type = "list(string)" }
  }
`)
	if g.Contracts == nil {
		t.Fatal("group's own contracts were not merged into the graph")
	}
	found := false
	for _, p := range Validate(g) {
		if strings.Contains(p.Message, "[C003]") {
			found = true
			if p.IsError() {
				t.Fatalf("contract problems must stay warnings: %s", p.Message)
			}
		}
	}
	if !found {
		t.Fatal("expected C003 on the edge expanded from the group instance, checked against the group's internal promise")
	}
}

// TestBuild_GroupContractUndeclaredPortFiresC001 proves a group's promise about its internal module is checked against the module itself: an output the module never declares is C001 even though the group encapsulates the node, because the check keys on the internal node's real directory.
func TestBuild_GroupContractUndeclaredPortFiresC001(t *testing.T) {
	g := writeGroupContractsFixture(t, `
  producer "./vpc" {
    output "ghost" { type = "string" }
  }
`)
	found := false
	for _, p := range Validate(g) {
		if strings.Contains(p.Message, "[C001]") && strings.Contains(p.Message, "ghost") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected C001 for the group's promise about a port its internal module never declared")
	}
}

// TestBuild_IdenticalContractsAcrossGroupAndRootDedupe proves the merge rule for legit reuse: the root blueprint and a group both promising the same thing about the same module directory dedupe silently into one record.
func TestBuild_IdenticalContractsAcrossGroupAndRootDedupe(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "modules/net/vpc/main.tf"), `output "id" { value = "x" }`)
	writeFixtureFile(t, filepath.Join(root, "modules/net/group.hcl"), `
group "net" {
  node "vpc" { source = "./vpc" }
  producer "./vpc" {
    output "id" { type = "string" }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "net" {
  as     = "net"
  source = "./modules/net"
}
producer "./modules/net/vpc" {
  output "id" { type = "string" }
}
`)
	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("identical claims must dedupe, got: %v", err)
	}
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("got = %v, want zero problems after dedupe", problems)
	}
}

// TestBuild_ConflictingContractsAcrossGroupAndRootError proves the other half of the merge rule: the same (source, role, port) with different claims in the blueprint and in a group is a Build error, never a silent winner.
func TestBuild_ConflictingContractsAcrossGroupAndRootError(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "modules/net/vpc/main.tf"), `output "id" { value = "x" }`)
	writeFixtureFile(t, filepath.Join(root, "modules/net/group.hcl"), `
group "net" {
  node "vpc" { source = "./vpc" }
  producer "./vpc" {
    output "id" { type = "string" }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
use "net" {
  as     = "net"
  source = "./modules/net"
}
producer "./modules/net/vpc" {
  output "id" { type = "number" }
}
`)
	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	_, err = Build(bp, root)
	if err == nil || !strings.Contains(err.Error(), "declared both in the blueprint and in a group") {
		t.Fatalf("got = %v, want conflicting-claims Build error", err)
	}
}

// writeReconcileFixture is writeContractsFixture with the two module declarations as parameters — the reconciliation checks (C007–C009) compare contract claims against exactly this module reality, so each case must state its own.
func writeReconcileFixture(t *testing.T, vpcOutput, appVariable, contractsHCL string) *Graph {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "modules/vpc/main.tf"), vpcOutput)
	writeFixtureFile(t, filepath.Join(root, "modules/app/main.tf"), appVariable+`
output "ok" { value = var.vpc_id }
`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "vpc" { source = "./modules/vpc" }
node "app" { source = "./modules/app" }
edge {
  from = node.vpc.output.vpc_id
  to   = node.app.input.vpc_id
}
`+contractsHCL)
	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return g
}

// TestValidate_ContractTypeLieAgainstModuleFiresC007 is the review's Repro A: the consumer contract claims string and the producer contract agrees (so C003 stays silent — the two sides are compatible), but the module itself declares number. The contract is wrong, not the wiring: C007 fires as a warning and escalates to an error under enforce, on the same dial as every other code.
func TestValidate_ContractTypeLieAgainstModuleFiresC007(t *testing.T) {
	g := writeReconcileFixture(t,
		`output "vpc_id" { value = "x" }`,
		`variable "vpc_id" { type = number }`,
		`
producer "./modules/vpc" {
  output "vpc_id" { type = "string" }
}
consumer "./modules/app" {
  input "vpc_id" { type = "string" }
}
`)
	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("got = %v, want exactly one problem", problems)
	}
	p := problems[0]
	if !strings.Contains(p.Message, "[C007]") || !strings.Contains(p.Message, "./modules/app") || !strings.Contains(p.Message, "number") || p.IsError() {
		t.Fatalf("got = %+v, want C007 warning naming the consumer scope and the module's number", p)
	}
	g.ContractMode = "enforce"
	if problems := Validate(g); len(problems) != 1 || !problems[0].IsError() {
		t.Fatalf("enforce must escalate C007 to an error: %v", problems)
	}
}

// TestValidate_ProducerSensitiveLieAgainstModuleFiresC009 is the review's Repro B: the module marks the output sensitive but the producer contract promises sensitive = false. The module is the declaration of record, so the contract is the thing to fix.
func TestValidate_ProducerSensitiveLieAgainstModuleFiresC009(t *testing.T) {
	g := writeReconcileFixture(t,
		`output "vpc_id" {
  value     = "x"
  sensitive = true
}`,
		`variable "vpc_id" { type = string }`,
		`
producer "./modules/vpc" {
  output "vpc_id" { sensitive = false }
}
`)
	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("got = %v, want exactly one problem", problems)
	}
	if p := problems[0]; !strings.Contains(p.Message, "[C009]") || !strings.Contains(p.Message, "./modules/vpc") || p.IsError() {
		t.Fatalf("got = %+v, want C009 warning naming the producer scope", p)
	}
}

// TestValidate_ConsumerSensitiveLieAgainstModuleFiresC008 proves the input-side twin of C009: the module declares the variable sensitive, the consumer contract claims it is not — an explicit false is a claim, and it contradicts the module in either direction.
func TestValidate_ConsumerSensitiveLieAgainstModuleFiresC008(t *testing.T) {
	g := writeReconcileFixture(t,
		`output "vpc_id" { value = "x" }`,
		`variable "vpc_id" {
  type      = string
  sensitive = true
}`,
		`
consumer "./modules/app" {
  input "vpc_id" { sensitive = false }
}
`)
	problems := Validate(g)
	if len(problems) != 1 {
		t.Fatalf("got = %v, want exactly one problem", problems)
	}
	if p := problems[0]; !strings.Contains(p.Message, "[C008]") || !strings.Contains(p.Message, "./modules/app") || p.IsError() {
		t.Fatalf("got = %+v, want C008 warning naming the consumer scope", p)
	}
}

// TestValidate_ClaimsMatchingModuleSchemaAreSilent proves reconciliation is not a tax on honest contracts: type and sensitive claims that agree with the module schema on both roles produce zero problems.
func TestValidate_ClaimsMatchingModuleSchemaAreSilent(t *testing.T) {
	g := writeReconcileFixture(t,
		`output "vpc_id" {
  value     = "x"
  sensitive = true
}`,
		`variable "vpc_id" {
  type      = string
  sensitive = true
}`,
		`
producer "./modules/vpc" {
  output "vpc_id" {
    type      = "string"
    sensitive = true
  }
}
consumer "./modules/app" {
  input "vpc_id" {
    type      = "string"
    sensitive = true
  }
}
`)
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("got = %v, want zero problems when claims match the schema", problems)
	}
}

// TestValidate_UnconstrainedVariableSkipsTypeReconciliation proves the explicit-claims-only rule on the module side: a variable with no type constraint has nothing to contradict, so a consumer type claim against it fires nothing.
func TestValidate_UnconstrainedVariableSkipsTypeReconciliation(t *testing.T) {
	g := writeReconcileFixture(t,
		`output "vpc_id" { value = "x" }`,
		`variable "vpc_id" {}`,
		`
consumer "./modules/app" {
  input "vpc_id" { type = "string" }
}
`)
	if problems := Validate(g); len(problems) != 0 {
		t.Fatalf("got = %v, want zero problems against an unconstrained variable", problems)
	}
}
