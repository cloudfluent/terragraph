//go:build !windows

package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// loadTFVarsCleanupEngine builds a one-node engine whose node actually receives input values
// (via vars), so Plan/Apply/Destroy genuinely write the tfvars file the cleanup defer removes;
// without inputs WriteTFVars writes nothing and the absence assertion below would pass vacuously.
func loadTFVarsCleanupEngine(t *testing.T) *Engine {
	t.Helper()
	baseDir := t.TempDir()
	moduleDir := filepath.Join(baseDir, "module")
	writeModule(t, moduleDir)
	// The variable vars feeds, and payload.txt: the fake terraform's apply copies it into the
	// saved plan (see writeFakeTerraform). managed.out starts as payload.txt's twin so a plain
	// Plan (no -detailed-exitcode; any nonzero exit is an error there) reports no changes.
	if err := os.WriteFile(filepath.Join(moduleDir, "variables.tf"), []byte("variable \"payload\" {\n  type = string\n}\n"), 0o644); err != nil {
		t.Fatalf("writing fixture variables: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "payload.txt"), []byte("version-one\n"), 0o644); err != nil {
		t.Fatalf("writing payload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "managed.out"), []byte("version-one\n"), 0o644); err != nil {
		t.Fatalf("writing managed output: %v", err)
	}
	blueprintPath := writeBlueprint(t, baseDir, `
node "app" {
  source = "./module"
  vars = { payload = "version-one" }
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

// assertNoTFVars fails unless the node's tfvars file is gone: the file holds resolved input
// values in cleartext, so a run that ends must not leave it behind on a shared runner.
func assertNoTFVars(t *testing.T, e *Engine, node string) {
	t.Helper()
	if _, err := os.Stat(e.tfVarsPath(node)); !os.IsNotExist(err) {
		t.Fatalf("tfvars file for node %s still present at %s after the run ended (stat err = %v)", node, e.tfVarsPath(node), err)
	}
}

func TestPlan_RemovesTFVarsWhenRunEnds(t *testing.T) {
	e := loadTFVarsCleanupEngine(t)

	if err := e.Plan(Options{}); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertNoTFVars(t, e, "app")
}

func TestApply_RemovesTFVarsWhenRunEnds(t *testing.T) {
	e := loadTFVarsCleanupEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertNoTFVars(t, e, "app")
}
