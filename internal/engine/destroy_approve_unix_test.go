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

// A node that declared approve = "none" has said it must not be torn down, and destroy refuses before taking any lock. The declaration is what is read, not the resolved level: reading approveFor would fill blanks down to "safe" and refuse every ordinary node instead.
func TestDestroy_RefusesANodeThatDeclaredLessThanAll(t *testing.T) {
	e, commandLog := loadDestroyApproveEngine(t, `
node "guarded" {
  source  = "./module"
  approve = "none"
}
`)

	_, err := e.Destroy(Options{AutoApprove: true})
	if err == nil {
		t.Fatal(`expected destroy to be refused at approve = "none"`)
	}
	for _, want := range []string{"guarded", `approve = "none"`, `"all"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected the error to mention %q, got: %v", want, err)
		}
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

// The declaration holds whether or not anyone is watching, the same rule apply's gate follows: an interactive "yes" answers "tear down this node", not "override the standing policy". Without this, approve = "none" protected nothing the moment someone was at the terminal.
func TestDestroy_RefusesADeclaredNodeEvenInteractively(t *testing.T) {
	e, _ := loadDestroyApproveEngine(t, `
node "guarded" {
  source  = "./module"
  approve = "none"
}
`)

	e.Stdin = strings.NewReader("yes\n")
	if _, err := e.Destroy(Options{}); err == nil {
		t.Fatal(`expected an interactive destroy to be refused at approve = "none"`)
	}
}

// Nothing in the node's chain ever spoke, so nothing is being overridden and teardown proceeds — the ordinary blueprint, unchanged by this gate.
func TestDestroy_PermitsANodeThatDeclaredNothing(t *testing.T) {
	e, _ := loadDestroyApproveEngine(t, `
node "guarded" { source = "./module" }
`)

	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := e.Destroy(Options{AutoApprove: true}); err != nil {
		t.Fatalf("expected teardown of an undeclared node to proceed: %v", err)
	}
}

func TestDestroy_ConsumerContractRefusesEnhancedBackendBeforePlan(t *testing.T) {
	for _, backend := range []string{"remote", "cloud"} {
		t.Run(backend, func(t *testing.T) {
			e, commandLog := loadDestroyApproveEngine(t, `
node "guarded" { source = "./module" }
consumer "./module" {
 input "value" { type = string }
}
`)
			if err := osWriteFile(filepath.Join(e.BaseDir, "module", "variables.tf"), []byte(`variable "value" { default = "ok" }`)); err != nil {
				t.Fatal(err)
			}
			e, err := Load(filepath.Join(e.BaseDir, "blueprint.hcl"), e.Binary, &bytes.Buffer{}, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("TG_BACKEND_TYPE", backend)
			_, err = e.Destroy(Options{AutoApprove: true})
			if err == nil {
				t.Fatal("expected enhanced backend refusal")
			}
			for _, want := range []string{backend, "destroy", "local plan", "s3"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("got = %v, want %q", err, want)
				}
			}
			data, err := os.ReadFile(commandLog)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(data) != 0 {
				t.Fatalf("got = %q, want no plan, apply, or destroy command", data)
			}
		})
	}
}
