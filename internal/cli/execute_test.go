package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/engine"
)

func TestExecute_FlagErrorsEmitOneJSONResult(t *testing.T) {
	for _, args := range [][]string{
		{"apply", "--output", "json", "--parallelism", "bad"},
		{"apply", "--parallelism", "bad", "--output=json"},
		{"validate", "--unknown", "--output", "json"},
		{"plan", "recover", "--output", "json"},
		{"validate", "--log-level", "bad", "--output=json"},
		{"output", "--raw", "--output=json"},
		{"plan", "show", "run-missing", "--backup", "--output=json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, _, err := runRootCmd(t, args...)
			if err == nil {
				t.Fatal("got success, want argument failure")
			}
			var result errorResultDTO
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatalf("got = %q, %v", stdout, err)
			}
			if result.SchemaVersion != 1 || len(result.Diagnostics) == 0 || result.Diagnostics[0].Category != "arguments" || result.Diagnostics[0].Severity != "error" {
				t.Fatalf("got = %+v, want argument diagnostics", result)
			}
		})
	}
}

func TestExecute_DoesNotInterpretFlagValuesAsJSONSelection(t *testing.T) {
	for _, args := range [][]string{
		{"validate", "--blueprint", "--output=json"},
		{"validate", "--output", "xml"},
		{"validate", "--output", "text", "--output=json", "--unknown"},
		{"run", "--node", "a", "--", "--output=json"},
		{"force-unlock", "--output=json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, _, err := runRootCmd(t, args...)
			if err == nil || json.Valid([]byte(stdout)) {
				t.Fatalf("got = %q, %v, want legacy failure", stdout, err)
			}
		})
	}
}

func TestExecute_LoadFailureDoesNotClaimValidationOrExecution(t *testing.T) {
	stdout, _, err := runRootCmd(t, "validate", "--blueprint", filepath.Join(t.TempDir(), "missing.hcl"), "--output", "json")
	if err == nil {
		t.Fatal("got success")
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result["valid"] != nil || result["execution_id"] != nil || result["diagnostics"] == nil {
		t.Fatalf("got = %s", stdout)
	}
}

func TestExecute_ValidationIncludesStructuredProblems(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), `node "a" { source = "./module" }
node "b" { source = "./module" }
edge {
 from = node.a.output.absent
 to = node.b.input.absent
}`)
	writeFixtureFile(t, filepath.Join(dir, "module", "main.tf"), `output "present" { value = "ok" }`)
	for _, command := range []string{"validate", "graph", "plan", "apply", "destroy"} {
		t.Run(command, func(t *testing.T) {
			args := []string{command, "--blueprint", dir, "--output=json"}
			if command == "apply" || command == "destroy" {
				args = append(args, "--auto-approve")
			}
			stdout, _, err := runRootCmd(t, args...)
			if err == nil || !strings.Contains(stdout, `"code":"missing_output"`) || !strings.Contains(stdout, `"severity":"error"`) || strings.Contains(stdout, "execution_id") {
				t.Fatalf("got = %s, %v", stdout, err)
			}
		})
	}
}

type failingResultWriter struct{ calls int }

func (w *failingResultWriter) Write([]byte) (int, error) { w.calls++; return 0, io.ErrClosedPipe }

func TestExecute_WriterFailureDoesNotEmitAgain(t *testing.T) {
	root := NewRootCmd("test")
	out := &failingResultWriter{}
	root.SetOut(out)
	root.SetErr(io.Discard)
	err := Execute(context.Background(), root, []string{"validate", "--blueprint", "../../examples/group/blueprint.hcl", "--output=json"})
	if !errors.Is(err, io.ErrClosedPipe) || out.calls != 1 {
		t.Fatalf("got = %d writes, %v", out.calls, err)
	}
}

func TestFinishRun_PreservesIndependentJournalFailure(t *testing.T) {
	nodeErr := errors.New("native failed")
	recordErr := engine.WithDiagnostic(errors.New("store unavailable"), engine.Diagnostic{Code: "execution_record_write_failed", Category: "record", Phase: "record"})
	root := NewRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	result := engine.RunResult{ExecutionID: "run-original", Nodes: []engine.NodeRun{{Node: "a", Status: engine.StatusFailed, Err: nodeErr, Diagnostics: engine.Diagnostics(nodeErr, engine.Diagnostic{Code: "runtime_failed", Category: "runtime"})}}}
	if err := finishRun(root, "json", result, fmt.Errorf("execution: %w", errors.Join(nodeErr, recordErr))); err == nil {
		t.Fatal("got success")
	}
	var got runResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ExecutionID != result.ExecutionID || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "execution_record_write_failed" || len(got.Nodes[0].Diagnostics) != 1 {
		t.Fatalf("got = %s", out.String())
	}
}
