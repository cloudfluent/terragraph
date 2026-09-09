package engine

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func savedRuntimeFixture(t *testing.T) *Engine {
	t.Helper()
	binary := os.Getenv("TERRAGRAPH_TEST_RUNTIME")
	if binary == "" {
		t.Skip("set TERRAGRAPH_TEST_RUNTIME for native retained-plan evidence")
	}
	dir := t.TempDir()
	for _, name := range []string{"upstream", "downstream"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"blueprint.hcl": `node "upstream" { source = "./upstream" }
node "downstream" { source = "./downstream" }
edge {
 from = node.upstream.output.value
 to = node.downstream.input.input
}
`,
		filepath.Join("upstream", "main.tf"): `terraform {
 backend "local" {}
}
output "value" { value = "real-upstream" }
`,
		filepath.Join("downstream", "main.tf"): `terraform {
 backend "local" {}
}
variable "input" { type = string }
output "value" { value = var.input }
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	e, err := Load(filepath.Join(dir, "blueprint.hcl"), exec.Binary(binary), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSavedExecution_RealFrontierAndExactApplication(t *testing.T) {
	e := savedRuntimeFixture(t)
	record, err := e.SavePlans(Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	phases := map[string]string{}
	for _, node := range record.Nodes {
		phases[node.Name] = node.Phase
	}
	if phases["upstream"] != "planned" || phases["downstream"] != "pending" {
		t.Fatalf("got = %v", phases)
	}
	if _, err := os.Stat(filepath.Join(e.BaseDir, ".terragraph", "state", "upstream.tfstate")); !os.IsNotExist(err) {
		t.Fatal("saving plan mutated state")
	}
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err == nil {
		t.Fatal("consumed frontier replayed")
	}
	next, err := e.SavePlans(Options{}, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.Status != "waiting_for_apply" {
		t.Fatalf("got = %s", next.Status)
	}
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	outputs, err := e.runner("downstream").Outputs()
	if err != nil || outputs["value"].Value != "real-upstream" {
		t.Fatalf("got = %+v, %v", outputs, err)
	}
	final, err := e.GetExecution(record.ID)
	if err != nil || final.Status != "completed" {
		t.Fatalf("got = %+v, %v", final, err)
	}
	store, err := e.openExecutionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.close() }()
	keys, err := store.list(e.context())
	if err != nil || len(keys) != 1 {
		t.Fatalf("completed bundle cleanup: %v, %v", keys, err)
	}
}

func TestSavedExecution_RealChangedSourceRefused(t *testing.T) {
	e := savedRuntimeFixture(t)
	record, err := e.SavePlans(Options{Nodes: []string{"upstream"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.nodeDir("upstream"), "payload.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err == nil {
		t.Fatal("changed source accepted")
	}
	if _, err := os.Stat(filepath.Join(e.BaseDir, ".terragraph", "state", "upstream.tfstate")); !os.IsNotExist(err) {
		t.Fatal("changed source reached apply")
	}
}

func TestApply_RealRetainedOptionDoesNotPause(t *testing.T) {
	e := savedRuntimeFixture(t)
	if _, err := e.Apply(Options{AutoApprove: true, RetainPlan: true}); err != nil {
		t.Fatal(err)
	}
	outputs, err := e.runner("downstream").Outputs()
	if err != nil || outputs["value"].Value != "real-upstream" {
		t.Fatalf("got = %+v, %v", outputs, err)
	}
}

func TestSavedExecution_RealContinuationRejectsChangedUpstreamSource(t *testing.T) {
	e := savedRuntimeFixture(t)
	record, err := e.SavePlans(Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.nodeDir("upstream"), "changed.txt"), []byte("changed after apply"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SavePlans(Options{}, record.ID); err == nil {
		t.Fatal("continuation accepted changed upstream source")
	}
}
