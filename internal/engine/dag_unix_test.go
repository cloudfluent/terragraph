//go:build !windows

package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApply_NodeTimeoutStopsRuntimeAndCleansVariables(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	path := string(e.Binary)
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script = bytes.Replace(script, []byte("init)"), []byte("init)\n trap 'exit 130' INT\n sleep 30\n"), 1)
	if err := os.WriteFile(path, script, 0700); err != nil {
		t.Fatal(err)
	}
	runs, err := e.Apply(Options{AutoApprove: true, NodeTimeout: 100 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || len(runs) != 1 || runs[0].Reason != "timeout" {
		t.Fatalf("runs = %+v, err = %v", runs, err)
	}
	if _, err := os.Stat(e.tfVarsPath("cached")); !os.IsNotExist(err) {
		t.Fatalf("variables survived deadline: %v", err)
	}
}

func TestApply_TimedInteractiveRunIsRefused(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	_, err := e.Apply(Options{NodeTimeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "timeouts need --auto-approve") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRecord_PrivateReceiptPermissions(t *testing.T) {
	e := recordFixture(t)
	if err := e.writeRunRecord("apply", []NodeRun{{Node: "root", Status: StatusRunning}}); err != nil {
		t.Fatal(err)
	}
	path, err := e.recordPath("apply")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := e.writeRunRecord("apply", []NodeRun{{Node: "root", Status: StatusFailed}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("receipt info = %v, error = %v, want owner-only replacement", info, err)
	}
}

func TestApply_ResumeReplansAndUsesFreshUpstreamOutputs(t *testing.T) {
	e := loadFallbackEngine(t, false)
	path := string(e.Binary)
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script = bytes.Replace(script, []byte("case \"$1\" in"), []byte(`name=$(basename "$TF_DATA_DIR")
printf '%s %s\n' "$name" "$1" >> "$TG_DAG_COMMANDS"
if [ "$1" = plan ] && [ "$name" = "${TG_DAG_FAIL_NODE:-}" ]; then exit 1; fi
case "$1" in`), 1)
	if err := os.WriteFile(path, script, 0700); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(e.BaseDir, "dag-calls")
	t.Setenv("TG_DAG_COMMANDS", calls)
	t.Setenv("TG_DAG_FAIL_NODE", "b")
	runs, err := e.Apply(Options{AutoApprove: true, RecordRun: true})
	if err == nil || len(runs) != 2 || runs[0].Status != StatusApplied || runs[1].Status != StatusFailed {
		t.Fatalf("first runs = %+v, err = %v", runs, err)
	}
	t.Setenv("TG_DAG_FAIL_NODE", "")
	t.Setenv("TG_OUTPUT_LATER", "fresh-resumed-output")
	if err := os.WriteFile(calls, nil, 0600); err != nil {
		t.Fatal(err)
	}
	runs, err = e.Apply(Options{AutoApprove: true, Resume: true})
	if err != nil || len(runs) != 2 {
		t.Fatalf("resumed runs = %+v, err = %v", runs, err)
	}
	commands, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"a plan\n", "a apply\n", "b plan\n", "b apply\n"} {
		if strings.Count(string(commands), command) != 1 {
			t.Fatalf("calls = %s, want one %q", commands, command)
		}
	}
	if seen := varfileSeen(t, e, "b"); !strings.Contains(seen, "fresh-resumed-output") {
		t.Fatalf("resumed input = %s, want fresh upstream value", seen)
	}
}
