//go:build !windows

package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// loadNodeGuardTestEngine builds the smallest graph the defect needs: a data edge a.output.managed -> b.input.payload, on the shared fake terraform (whose `output` reports "managed" and whose destroy succeeds under -auto-approve).
func loadNodeGuardTestEngine(t *testing.T) *Engine {
	t.Helper()
	baseDir := t.TempDir()
	moduleDir := filepath.Join(baseDir, "module")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", moduleDir, err)
	}
	src := "terraform {\n  backend \"local\" {}\n}\noutput \"managed\" {\n  value = \"ok\"\n}\nvariable \"payload\" {\n  type = string\n}\n"
	if err := os.WriteFile(filepath.Join(moduleDir, "main.tf"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing fixture module: %v", err)
	}
	blueprintPath := writeBlueprint(t, baseDir, `
node "a" { source = "./module" }
node "b" { source = "./module" }

edge {
  from = node.a.output.managed
  to   = node.b.input.payload
}
`)
	t.Setenv("TG_COMMAND_LOG", filepath.Join(baseDir, "commands.log"))
	t.Setenv("TG_PLAN_ERROR", filepath.Join(baseDir, "plan-error"))

	e, err := Load(blueprintPath, exec.Binary(writeFakeTerraform(t, baseDir)), &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return e
}

// Destroying an upstream node --node while a consumer still reads its outputs would strand the graph: the consumer's next resolution dies with terraform's misleading "has no output value; apply it first". The refusal names the consumers and the way out.
func TestDestroy_NodeRefusedWhileConsumersReadItsOutputs(t *testing.T) {
	e := loadNodeGuardTestEngine(t)

	err := e.Destroy(Options{Node: "a", AutoApprove: true})
	if err == nil {
		t.Fatal("expected --node a destroy to be refused while b still consumes its outputs")
	}
	for _, want := range []string{`still feeds b;`, "destroy consumers first"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got: %v", want, err)
		}
	}
}

// The consumer itself has nobody downstream: destroying it alone is exactly the right order.
func TestDestroy_NodeWithoutDownstreamConsumersProceeds(t *testing.T) {
	e := loadNodeGuardTestEngine(t)

	if err := e.Destroy(Options{Node: "b", AutoApprove: true}); err != nil {
		t.Fatalf("Destroy --node b: %v", err)
	}
}

// A full destroy needs no guard of its own: reverse topological order tears b down before a.
func TestDestroy_FullDestroyTearsDownConsumersFirst(t *testing.T) {
	e := loadNodeGuardTestEngine(t)

	if err := e.Destroy(Options{AutoApprove: true}); err != nil {
		t.Fatalf("full Destroy: %v", err)
	}
}
