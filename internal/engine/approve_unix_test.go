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

	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	if err := os.Truncate(commandLog, 0); err != nil {
		t.Fatalf("truncating command log: %v", err)
	}
	t.Setenv("TG_PLAN_ACTIONS", `"delete"`)

	_, err := e.Apply(Options{AutoApprove: true})
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

	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	t.Setenv("TG_PLAN_ACTIONS", `"delete","create"`)

	_, err := e.Apply(Options{AutoApprove: true})
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

	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	t.Setenv("TG_PLAN_ACTIONS", `"create"`)

	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("expected a create-only plan to be permitted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("expected the drifted resource to be recreated: %v", err)
	}
}

func TestApply_DestructivePlanIsAllowedByTheRunLevel(t *testing.T) {
	e, moduleDir, _ := loadApplyTestEngine(t)

	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	t.Setenv("TG_PLAN_ACTIONS", `"delete"`)

	if _, err := e.Apply(Options{AutoApprove: true, Approve: blueprint.ApproveAll}); err != nil {
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

	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := "node cached: 1 to add, 0 to change, 0 to destroy"; !strings.Contains(out.String(), want) {
		t.Fatalf("expected %q in output, got:\n%s", want, out.String())
	}
}

// destroy has no saved plan for terragraph to ask about, so terraform's own confirmation is the approval — which needs a stdin to read from, the same defect apply had.
func TestDestroy_WithoutAutoApproveReadsApprovalFromStdin(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	e.Stdin = strings.NewReader("yes\n")
	if _, err := e.Destroy(Options{}); err != nil {
		t.Fatalf("Destroy without auto-approve: %v", err)
	}
}

func TestDestroy_ParallelismRequiresAutoApprove(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	_, err := e.Destroy(Options{Parallelism: 2})
	if err == nil {
		t.Fatal("expected --parallelism without --auto-approve to be refused")
	}
	if !strings.Contains(err.Error(), "--auto-approve") {
		t.Fatalf("expected the error to name --auto-approve, got: %v", err)
	}
}

// Destroy cannot tell in advance whether it is about to ask a question, so a node that fails with no input available says why, rather than leaving terraform's bare "error asking for approval: EOF" as the whole explanation.
func TestDestroy_WithoutAutoApproveAndNoInputExplainsItself(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// A reader that yields nothing, not a nil one: this is what a CLI run redirected from /dev/null actually looks like, and testing for nil would miss it.
	e.Stdin = strings.NewReader("")
	_, err := e.Destroy(Options{})
	if err == nil {
		t.Fatal("expected destroy to fail when it cannot read approval")
	}
	if !strings.Contains(err.Error(), "--auto-approve") {
		t.Fatalf("expected the error to name --auto-approve, got: %v", err)
	}
}

// A refusal is a decision someone made, so it must not be dressed up as missing input.
func TestDestroy_DeclinedApprovalIsNotReportedAsMissingInput(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	e.Stdin = strings.NewReader("no\n")
	_, err := e.Destroy(Options{})
	if err == nil {
		t.Fatal("expected a declined destroy to fail")
	}
	if strings.Contains(err.Error(), "nothing was available") {
		t.Fatalf("a declined destroy should not be reported as missing input, got: %v", err)
	}
}

// An unattended destroy with nothing left to tear down still succeeds: terraform never asks, so there is no approval to be missing.
func TestDestroy_NoOpNeedsNoApproval(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	e.Stdin = strings.NewReader("yes\n")
	if _, err := e.Destroy(Options{}); err != nil {
		t.Fatalf("first Destroy: %v", err)
	}

	// Nothing left; the fake reports no-op without prompting, exactly as terraform does.
	t.Setenv("TG_DESTROY_NOOP", "1")
	e.Stdin = nil
	if _, err := e.Destroy(Options{}); err != nil {
		t.Fatalf("expected a no-op destroy to need no approval: %v", err)
	}
}
