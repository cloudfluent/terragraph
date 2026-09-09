package engine

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
	"github.com/zclconf/go-cty/cty"
)

func TestInputContractChecks_MissingSelectedValueDoesNotAssumeDefault(t *testing.T) {
	root := t.TempDir()
	if err := osWriteFile(filepath.Join(root, "m", "main.tf"), []byte("variable \"value\" {\n type = string\n default = \"fallback\"\n}\n")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "blueprint.hcl")
	if err := osWriteFile(path, []byte(`node "a" { source = "./m" }
consumer "./m" {
 input "value" { type = string }
}`)); err != nil {
		t.Fatal(err)
	}
	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	checks := e.inputContractChecks("a", &exec.PlanValues{})
	if len(checks) != 1 || checks[0].Result != blueprint.ContractDeferred {
		t.Fatalf("got = %+v, want deferred", checks)
	}
}

func TestContractPolicy_DoesNotExposeNestedKeys(t *testing.T) {
	e := &Engine{Graph: &graph.Graph{ContractMode: "enforce"}}
	value := cty.ObjectVal(map[string]cty.Value{"PRIVATE_KEY": cty.StringVal("PRIVATE_VALUE")})
	checks := checkPort("node.a.input.credentials", blueprint.PortContract{Type: "list(string)"}, value, new(true))
	err := e.contractPolicy(checks, true)
	if err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("got = %v, want redacted violation", err)
	}
}

func TestCheckVarType_ContractSensitivityProtectsConversionErrors(t *testing.T) {
	root := t.TempDir()
	if err := osWriteFile(filepath.Join(root, "m", "main.tf"), []byte(`variable "value" { type = map(number) }`)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "blueprint.hcl")
	if err := osWriteFile(path, []byte(`node "a" { source = "./m" }
consumer "./m" {
 input "value" { sensitive = true }
}`)); err != nil {
		t.Fatal(err)
	}
	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	err = e.checkVarType("a", "value", map[string]any{"PRIVATE_KEY": []any{"PRIVATE_VALUE"}}, false)
	if err == nil || strings.Contains(err.Error(), "PRIVATE") || !strings.Contains(err.Error(), "withheld") {
		t.Fatalf("got = %v, want protected conversion error", err)
	}
}
