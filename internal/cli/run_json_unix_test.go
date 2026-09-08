//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRunFakeTerraform emits identifiable output on both streams so JSON tests catch routing regressions; init and plan succeed unless TG_FAKE_PLAN_FAIL=1, and other commands fail.
func writeRunFakeTerraform(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "terraform-fake")
	script := `#!/bin/sh
printf 'terraform %s stdout\n' "$1"
printf 'terraform %s stderr\n' "$1" >&2
case "$1" in
  init) exit 0 ;;
  plan)
    if [ -n "${TG_FAKE_PLAN_FAIL:-}" ]; then
      exit 1
    fi
    exit 0
    ;;
esac
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake terraform: %v", err)
	}
	return path
}

// writeRunFixture builds a one-node blueprint whose runtime pins the fake terraform, so plan/apply/destroy run end to end through the real command tree without a terraform install.
func writeRunFixture(t *testing.T) string {
	t.Helper()
	baseDir := t.TempDir()
	fake := writeRunFakeTerraform(t, baseDir)
	writeFixtureFile(t, filepath.Join(baseDir, "module", "main.tf"),
		"terraform {\n  backend \"local\" {}\n}\noutput \"id\" {\n  value = \"x\"\n}\n")
	writeFixtureFile(t, filepath.Join(baseDir, "blueprint.hcl"), fmt.Sprintf(
		"runtime \"fake\" {\n  binary = %q\n}\nnode \"a\" {\n  source = \"./module\"\n  runtime = runtime.fake\n}\n", fake))
	return filepath.Join(baseDir, "blueprint.hcl")
}

func runCmdAt(t *testing.T, blueprintPath string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCmd("test")
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"--blueprint", blueprintPath}, args...))
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// assertRunDiagnostics requires both subprocess streams to reach stderr, catching output that is discarded as well as output that corrupts the JSON payload.
func assertRunDiagnostics(t *testing.T, stderr string, commands ...string) {
	t.Helper()
	for _, command := range commands {
		for _, stream := range []string{"stdout", "stderr"} {
			marker := fmt.Sprintf("terraform %s %s\n", command, stream)
			if !strings.Contains(stderr, marker) {
				t.Fatalf("stderr = %q, want Terraform output %q", stderr, marker)
			}
		}
	}
}

// TestPlan_OutputJSON_Planned proves stdout carries exactly one JSON document — no terraform output, no level headers — describing the node as planned.
func TestPlan_OutputJSON_Planned(t *testing.T) {
	bp := writeRunFixture(t)

	stdout, stderr, err := runCmdAt(t, bp, "plan", "--output", "json")
	if err != nil {
		t.Fatalf("plan --output json: %v", err)
	}
	assertRunDiagnostics(t, stderr, "init", "plan")
	if strings.Contains(stdout, "===") {
		t.Fatalf("stdout carries non-JSON run output: %q", stdout)
	}
	var got runResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%q", err, stdout)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].Node != "a" || got.Nodes[0].Level != 1 || got.Nodes[0].Status != "planned" || got.Nodes[0].Error != "" {
		t.Fatalf("got = %+v, want node a planned at level 1", got.Nodes)
	}
}

// TestPlan_OutputJSON_FailedNodeStillReports proves a failed run still emits the report — per-node outcomes are what automation reads after a failure — and that the run error still propagates for the exit code.
func TestPlan_OutputJSON_FailedNodeStillReports(t *testing.T) {
	bp := writeRunFixture(t)
	t.Setenv("TG_FAKE_PLAN_FAIL", "1")

	stdout, stderr, err := runCmdAt(t, bp, "plan", "--output", "json")
	if err == nil {
		t.Fatal("expected the failed plan to fail the command")
	}
	assertRunDiagnostics(t, stderr, "init", "plan")
	var got runResult
	if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%q", jerr, stdout)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].Status != "failed" || got.Nodes[0].Error == "" {
		t.Fatalf("got = %+v, want node a failed with an error message", got.Nodes)
	}
}

// TestApply_OutputJSON_FailedNodeStillReports proves apply reports the failing node in JSON even though its error also fails the command.
func TestApply_OutputJSON_FailedNodeStillReports(t *testing.T) {
	bp := writeRunFixture(t)
	t.Setenv("TG_FAKE_PLAN_FAIL", "1")

	stdout, stderr, err := runCmdAt(t, bp, "apply", "--output", "json", "--auto-approve")
	if err == nil {
		t.Fatal("expected the failed plan to fail the apply")
	}
	assertRunDiagnostics(t, stderr, "init", "plan")
	var got runResult
	if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%q", jerr, stdout)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].Status != "failed" || got.Nodes[0].Error == "" {
		t.Fatalf("got = %+v, want node a failed with an error message", got.Nodes)
	}
}

// TestDestroy_OutputJSON_FailedNodeStillReports proves destroy reports the failing node in JSON even though its error also fails the command.
func TestDestroy_OutputJSON_FailedNodeStillReports(t *testing.T) {
	bp := writeRunFixture(t)

	stdout, stderr, err := runCmdAt(t, bp, "destroy", "--output", "json", "--auto-approve")
	if err == nil {
		t.Fatal("expected the failing destroy to fail the command")
	}
	assertRunDiagnostics(t, stderr, "destroy")
	var got runResult
	if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%q", jerr, stdout)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].Status != "failed" || got.Nodes[0].Error == "" {
		t.Fatalf("got = %+v, want node a failed with an error message", got.Nodes)
	}
}
