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

// writeRunFakeTerraform writes a stand-in terraform that succeeds at init and plan only; TG_FAKE_PLAN_FAIL=1 makes plan exit 1, so a test can drive the failed-node path without a real binary.
func writeRunFakeTerraform(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "terraform-fake")
	script := `#!/bin/sh
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

// TestPlan_OutputJSON_Planned proves stdout carries exactly one JSON document — no terraform output, no level headers — describing the node as planned.
func TestPlan_OutputJSON_Planned(t *testing.T) {
	bp := writeRunFixture(t)

	stdout, _, err := runCmdAt(t, bp, "plan", "--output", "json")
	if err != nil {
		t.Fatalf("plan --output json: %v", err)
	}
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

	stdout, _, err := runCmdAt(t, bp, "plan", "--output", "json")
	if err == nil {
		t.Fatal("expected the failed plan to fail the command")
	}
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

	stdout, _, err := runCmdAt(t, bp, "apply", "--output", "json", "--auto-approve")
	if err == nil {
		t.Fatal("expected the failed plan to fail the apply")
	}
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

	stdout, _, err := runCmdAt(t, bp, "destroy", "--output", "json", "--auto-approve")
	if err == nil {
		t.Fatal("expected the failing destroy to fail the command")
	}
	var got runResult
	if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%q", jerr, stdout)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].Status != "failed" || got.Nodes[0].Error == "" {
		t.Fatalf("got = %+v, want node a failed with an error message", got.Nodes)
	}
}
