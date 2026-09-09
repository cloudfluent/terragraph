package engine

import (
	"bytes"
	"github.com/cloudfluent/terragraph/internal/blueprint"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func contractRuntimeFixture(t *testing.T, producerValue, variable, contracts string) *Engine {
	t.Helper()
	binary := os.Getenv("TERRAGRAPH_TEST_RUNTIME")
	if binary == "" {
		t.Skip("set TERRAGRAPH_TEST_RUNTIME for native contract evidence")
	}
	root := t.TempDir()
	for _, name := range []string{"producer", "consumer"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"blueprint.hcl": `contracts { mode = "enforce" }
node "producer" { source = "./producer" }
node "consumer" { source = "./consumer" }
edge {
 from = node.producer.output.value
 to = node.consumer.input.value
}
` + contracts,
		filepath.Join("producer", "main.tf"): `terraform {
 backend "local" {}
}
output "value" {
 description = "Contract test output."
 value = ` + producerValue + `
}
output "sentinel" { value = true }
`,
		filepath.Join("consumer", "main.tf"): `terraform {
 backend "local" {}
}
variable "value" {
 description = "Contract test input."
 ` + variable + `
}
output "received" { value = { value = var.value } }
`,
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	e, err := Load(filepath.Join(root, "blueprint.hcl"), exec.Binary(binary), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestContracts_RealRejectsProducerPrimitivePromise(t *testing.T) {
	e := contractRuntimeFixture(t, `123`, `type = string`, `producer "./producer" {
 output "value" { type = string }
}`)
	_, err := e.Apply(Options{AutoApprove: true})
	if err == nil || !strings.Contains(err.Error(), "C010") {
		t.Fatalf("got = %v, want producer contract violation", err)
	}
	if _, err := os.Stat(filepath.Join(e.BaseDir, ".terragraph", "state", "producer.tfstate")); !os.IsNotExist(err) {
		t.Fatalf("got = %v, want producer unchanged", err)
	}
}

func TestContracts_RealRejectsConsumerAdditionalConstraint(t *testing.T) {
	e := contractRuntimeFixture(t, `{ nested = { enabled = true } }`, `type = any`, `consumer "./consumer" {
 input "value" { type = map(string) }
}`)
	_, err := e.Apply(Options{AutoApprove: true})
	if err == nil || !strings.Contains(err.Error(), "C010") {
		t.Fatalf("got = %v, want consumer contract violation", err)
	}
	if _, err := os.Stat(filepath.Join(e.BaseDir, ".terragraph", "state", "consumer.tfstate")); !os.IsNotExist(err) {
		t.Fatalf("got = %v, want consumer unchanged", err)
	}
}

func TestContracts_RealNullAndDefaultAcrossRepeatedApply(t *testing.T) {
	e := contractRuntimeFixture(t, `null`, "type = string\ndefault = \"fallback\"\nnullable = false", `producer "./producer" {
 output "value" { type = string }
}
consumer "./consumer" {
 input "value" { nullable = false }
}`)
	for range 2 {
		if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
			t.Fatal(err)
		}
		outputs, err := e.runner("consumer").Outputs()
		if err != nil {
			t.Fatal(err)
		}
		if value := outputs["received"].Value.(map[string]any)["value"]; value != "fallback" {
			t.Fatalf("got = %v, want fallback", value)
		}
	}
}

func TestContracts_RealOptionalDefaultsAndProjection(t *testing.T) {
	e := contractRuntimeFixture(t, `{ ignored = 123 }`, `type = object({ enabled = optional(bool, true) })`, `consumer "./consumer" {
 input "value" { type = object({ enabled = bool }) }
}`)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	outputs, err := e.runner("consumer").Outputs()
	if err != nil {
		t.Fatal(err)
	}
	value := outputs["received"].Value.(map[string]any)["value"].(map[string]any)
	if value["enabled"] != true || len(value) != 1 {
		t.Fatalf("got = %v, want only enabled=true", value)
	}
}

func TestContracts_RealTupleSatisfiesList(t *testing.T) {
	e := contractRuntimeFixture(t, `["a", "b"]`, `type = list(string)`, `producer "./producer" {
 output "value" { type = list(string) }
}`)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
}

func TestContracts_RealExternalInputUsesModuleConversion(t *testing.T) {
	e := contractRuntimeFixture(t, `"upstream"`, `type = string`, `consumer "./consumer" {
 input "external" { type = number }
}`)
	path := filepath.Join(e.BaseDir, "consumer", "main.tf")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\nvariable \"external\" {\n type = string\n default = \"fallback\"\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.BaseDir, "consumer", "chosen.auto.tfvars"), []byte(`external = 123`), 0600); err != nil {
		t.Fatal(err)
	}
	e, err = Load(filepath.Join(e.BaseDir, "blueprint.hcl"), e.Binary, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Apply(Options{AutoApprove: true})
	if err == nil || !strings.Contains(err.Error(), "node.consumer.input.external: contract.[C010]") {
		t.Fatalf("got = %v, want rejection of effective string", err)
	}
}

func TestContracts_RealNoChangeStillChecksOutputs(t *testing.T) {
	e := contractRuntimeFixture(t, `123`, `type = string`, "")
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	e.Graph.Contracts = &blueprint.Contracts{ByDir: map[string]*blueprint.DirContracts{e.Graph.Nodes["producer"].Dir: {Producer: map[string]blueprint.PortContract{"value": {Type: "string"}}}}}
	_, err := e.Apply(Options{AutoApprove: true})
	if err == nil || !strings.Contains(err.Error(), "C010") {
		t.Fatalf("got = %v, want unchanged output rejection", err)
	}
}

func TestContracts_RealWarnReportsWithoutBlocking(t *testing.T) {
	e := contractRuntimeFixture(t, `123`, `type = string`, `producer "./producer" {
 output "value" { type = string }
}`)
	e.Graph.ContractMode = "warn"
	var log bytes.Buffer
	e.Logger = slog.New(slog.NewTextHandler(&log, nil))
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "C010") {
		t.Fatalf("got = %q, want contract diagnostic", log.String())
	}
}

func TestContracts_RealRetainedPlanRejectsChangedContract(t *testing.T) {
	e := contractRuntimeFixture(t, `"valid"`, `type = string`, `producer "./producer" {
 output "value" { type = string }
}`)
	record, err := e.SavePlans(Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	dc := e.nodeContracts("producer")
	p := dc.Producer["value"]
	p.Nullable = new(false)
	dc.Producer["value"] = p
	_, err = e.ApplySavedPlans(record.ID, Options{AutoApprove: true})
	if err == nil {
		t.Fatal("changed contract applied a retained plan")
	}
	if _, err := os.Stat(filepath.Join(e.BaseDir, ".terragraph", "state", "producer.tfstate")); !os.IsNotExist(err) {
		t.Fatalf("got = %v, want unchanged state", err)
	}
}

func TestContracts_RealPostApplyViolationRequiresOutputRecovery(t *testing.T) {
	e := contractRuntimeFixture(t, `terraform_data.computed.output`, `type = string`, `producer "./producer" {
 output "value" { type = string }
}`)
	path := filepath.Join(e.BaseDir, "producer", "main.tf")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("\nresource \"terraform_data\" \"computed\" { input = 123 }\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = e.Apply(Options{AutoApprove: true})
	if err == nil || !strings.Contains(err.Error(), "C010") {
		t.Fatalf("got = %v, want post-apply rejection", err)
	}
	records, err := e.ListExecutions()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("got = %d executions, want 1", len(records))
	}
	record := records[0]
	phase := ""
	for _, node := range record.Nodes {
		if node.Name == "producer" {
			phase = node.Phase
		}
	}
	if phase != "applied" {
		t.Fatalf("got = %s, want applied awaiting recovery", phase)
	}
	if _, err := e.Apply(Options{AutoApprove: true}); err == nil {
		t.Fatal("unresolved producer mutation was replayed")
	}
	dc := e.nodeContracts("producer")
	p := dc.Producer["value"]
	p.Type = "number"
	dc.Producer["value"] = p
	if _, err := e.RecoverExecution(record.ID, true, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.BaseDir, ".terragraph", "state", "consumer.tfstate")); !os.IsNotExist(err) {
		t.Fatalf("got = %v, want consumer never applied", err)
	}
}

func TestContracts_RealDestroyChecksInputsWithoutRequiringDeletedOutputs(t *testing.T) {
	e := contractRuntimeFixture(t, `"valid"`, `type = string`, `producer "./producer" {
 output "value" { type = string }
}
consumer "./consumer" {
 input "value" { type = string }
}`)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Destroy(Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
}

func TestContracts_RealSetMetadataSurvivesSnapshot(t *testing.T) {
	e := contractRuntimeFixture(t, `toset(["a", "b"])`, `type = set(string)`, `producer "./producer" {
 output "value" { type = set(string) }
}`)
	e.Graph.Snapshots = true
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	snapshot, ok := e.readSnapshot("producer")
	if !ok {
		t.Fatal("snapshot missing")
	}
	output := exec.Output{Value: snapshot.Outputs["value"], Type: snapshot.Types["value"], Sensitive: new(false)}
	if err := e.validateOutputContracts("producer", exec.Outputs{"value": output}); err != nil {
		t.Fatal(err)
	}
	output.Type = nil
	if err := e.validateOutputContracts("producer", exec.Outputs{"value": output}); err == nil || !strings.Contains(err.Error(), "C011") {
		t.Fatalf("got = %v, want missing type evidence", err)
	}
}

func TestContracts_RealParallelViolationDoesNotApplyDependent(t *testing.T) {
	e := contractRuntimeFixture(t, `123`, `type = string`, `producer "./producer" {
 output "value" { type = string }
}`)
	if _, err := e.Apply(Options{AutoApprove: true, Parallelism: 2}); err == nil {
		t.Fatal("parallel contract violation passed")
	}
	if _, err := os.Stat(filepath.Join(e.BaseDir, ".terragraph", "state", "consumer.tfstate")); !os.IsNotExist(err) {
		t.Fatalf("got = %v, want consumer unchanged", err)
	}
}

func TestContracts_RealRetainedFrontiersRecheckEffectiveInputs(t *testing.T) {
	e := contractRuntimeFixture(t, `{ nested = true }`, `type = any`, `consumer "./consumer" {
 input "value" { type = string }
}`)
	record, err := e.SavePlans(Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SavePlans(Options{}, record.ID); err == nil || !strings.Contains(err.Error(), "C010") {
		t.Fatalf("got = %v, want consumer frontier rejected", err)
	}
}
