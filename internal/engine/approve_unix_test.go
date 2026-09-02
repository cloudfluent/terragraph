//go:build !windows

package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// A destructive plan stops the node before anything is applied, even when nobody was going to be asked.
func TestApply_DestructivePlanIsRefusedAtTheDefaultLevel(t *testing.T) {
	e, moduleDir, commandLog := loadApplyTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	if err := os.Truncate(commandLog, 0); err != nil {
		t.Fatalf("truncating command log: %v", err)
	}
	t.Setenv("TG_PLAN_ACTIONS", `"delete"`)

	err := e.Apply(Options{AutoApprove: true})
	if err == nil {
		t.Fatal("expected a destroying plan to be refused at the default approve level")
	}
	for _, want := range []string{"does not permit", "fake.managed", "delete", "--approve=all"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected the error to mention %q, got: %v", want, err)
		}
	}
	data, readErr := os.ReadFile(commandLog)
	if readErr != nil {
		t.Fatalf("reading command log: %v", readErr)
	}
	if got := strings.Count(string(data), "apply\n"); got != 0 {
		t.Fatalf("apply count = %d, want 0; log:\n%s", got, data)
	}
}

// A replacement is destroy-and-recreate, and is what an upstream output change most often causes downstream, so it is refused for the same reason a plain delete is.
func TestApply_ReplacementIsRefusedAtTheDefaultLevel(t *testing.T) {
	e, moduleDir, _ := loadApplyTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	t.Setenv("TG_PLAN_ACTIONS", `"delete","create"`)

	err := e.Apply(Options{AutoApprove: true})
	if err == nil {
		t.Fatal("expected a replacing plan to be refused at the default approve level")
	}
	if !strings.Contains(err.Error(), "replace") {
		t.Fatalf("expected the error to name the action as a replace, got: %v", err)
	}
}

// Reconciling drift is a pure create, so the gate never gets in the way of the case #22 exists to catch.
func TestApply_DriftReconciliationPassesTheDefaultLevel(t *testing.T) {
	e, moduleDir, _ := loadApplyTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	t.Setenv("TG_PLAN_ACTIONS", `"create"`)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("expected a create-only plan to be permitted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("expected the drifted resource to be recreated: %v", err)
	}
}

func TestApply_DestructivePlanIsAllowedByTheRunLevel(t *testing.T) {
	e, moduleDir, _ := loadApplyTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	t.Setenv("TG_PLAN_ACTIONS", `"delete"`)

	if err := e.Apply(Options{AutoApprove: true, Approve: blueprint.ApproveAll}); err != nil {
		t.Fatalf("expected --approve=all to permit a destroying plan: %v", err)
	}
}

// The per-node declaration is the durable answer, and it is scoped to the node that needs it rather than the whole graph.
func TestApproveFor_NodeDeclarationWinsOverTheRunLevel(t *testing.T) {
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "db"))
	writeModule(t, filepath.Join(baseDir, "stacks", "vpc"))
	path := writeBlueprint(t, baseDir, `
node "db" {
  source  = "./stacks/db"
  approve = "all"
}
node "vpc" { source = "./stacks/vpc" }
`)
	e, err := Load(path, "terraform", &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := e.approveFor("db", blueprint.ApproveSafe); got != blueprint.ApproveAll {
		t.Fatalf("db approve = %q, want %q", got, blueprint.ApproveAll)
	}
	if got := e.approveFor("vpc", blueprint.ApproveSafe); got != blueprint.ApproveSafe {
		t.Fatalf("vpc approve = %q, want %q", got, blueprint.ApproveSafe)
	}
	// A CLI flag fills a gap; it never overrides what the blueprint said.
	if got := e.approveFor("db", blueprint.ApproveNone); got != blueprint.ApproveAll {
		t.Fatalf("db approve under --approve=none = %q, want %q", got, blueprint.ApproveAll)
	}
	if got := e.approveFor("vpc", blueprint.ApproveNone); got != blueprint.ApproveNone {
		t.Fatalf("vpc approve under --approve=none = %q, want %q", got, blueprint.ApproveNone)
	}
	// Nothing anywhere said one.
	if got := e.approveFor("vpc", ""); got != blueprint.ApproveSafe {
		t.Fatalf("vpc default approve = %q, want %q", got, blueprint.ApproveSafe)
	}
}

func TestApply_PrintsAPerNodeChangeSummary(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	out := &bytes.Buffer{}
	e.Stdout = out
	t.Setenv("TG_PLAN_ACTIONS", `"create"`)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := "node cached: 1 to add, 0 to change, 0 to destroy"; !strings.Contains(out.String(), want) {
		t.Fatalf("expected %q in output, got:\n%s", want, out.String())
	}
}
