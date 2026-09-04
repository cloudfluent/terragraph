//go:build !windows

package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// loadDestroyApproveEngine builds the standard one-node fake-terraform fixture with the node block left to the caller, so the destroy approve gate can be exercised end to end.
func loadDestroyApproveEngine(t *testing.T, node string) (*Engine, string) {
	t.Helper()
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "module"))
	if err := os.WriteFile(filepath.Join(baseDir, "module", "payload.txt"), []byte("version-one\n"), 0o644); err != nil {
		t.Fatalf("writing payload: %v", err)
	}
	path := writeBlueprint(t, baseDir, node)
	commandLog := filepath.Join(baseDir, "commands.log")
	t.Setenv("TG_COMMAND_LOG", commandLog)
	t.Setenv("TG_PLAN_ERROR", filepath.Join(baseDir, "plan-error"))

	e, err := Load(path, exec.Binary(writeFakeTerraform(t, baseDir)), &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return e, commandLog
}

// Teardown is delete-only, so an unattended destroy is permitted by approve = "all" alone; a node that said less is refused before anything runs, and --approve=all cannot speak for it, because the node's own declaration wins — the same layering apply already relies on.
func TestDestroy_ApproveGateRefusesUnattendedTeardown(t *testing.T) {
	e, commandLog := loadDestroyApproveEngine(t, `
node "guarded" {
  source  = "./module"
  approve = "none"
}
`)

	err := e.Destroy(Options{AutoApprove: true})
	if err == nil {
		t.Fatal(`expected an unattended destroy to be refused at approve = "none"`)
	}
	for _, want := range []string{"guarded", `approve = "none"`, `set approve = "all"`, "--auto-approve"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected the error to mention %q, got: %v", want, err)
		}
	}
	// The run-wide flag fills a gap; it never overrides what the node declared.
	if err := e.Destroy(Options{AutoApprove: true, Approve: blueprint.ApproveAll}); err == nil {
		t.Fatal(`expected --approve=all not to override the node's own approve = "none"`)
	}
	// Refused before any lock was taken, so nothing was touched — a log that never came into being is the strongest form of that.
	data, readErr := os.ReadFile(commandLog)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("reading command log: %v", readErr)
	}
	if got := strings.Count(string(data), "destroy\n"); got != 0 {
		t.Fatalf("destroy count = %d, want 0; log:\n%s", got, data)
	}
}

// The run-wide default fills a gap nothing else spoke to, so --approve=all permits unattended teardown of nodes that declared nothing — the one-off route for an intentional destroy.
func TestDestroy_ApproveGateRunLevelPermitsUnattendedTeardown(t *testing.T) {
	e, _ := loadDestroyApproveEngine(t, `
node "guarded" { source = "./module" }
`)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := e.Destroy(Options{AutoApprove: true, Approve: blueprint.ApproveAll}); err != nil {
		t.Fatalf("expected --approve=all to permit unattended teardown: %v", err)
	}
}

// A node declaring approve = "none" can still be torn down by a human: interactive destroy's gate is terraform's own confirmation prompt, which is exactly the someone-saying-so that approve levels defer the decision to.
func TestDestroy_ApproveGateInteractiveStaysHumanGated(t *testing.T) {
	e, _ := loadDestroyApproveEngine(t, `
node "guarded" {
  source  = "./module"
  approve = "none"
}
`)

	e.Stdin = strings.NewReader("yes\n")
	if err := e.Destroy(Options{}); err != nil {
		t.Fatalf("expected interactive destroy to be gated by terraform's prompt, not the approve level: %v", err)
	}
}
