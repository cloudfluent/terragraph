//go:build !windows

package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
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
	if !errors.Is(err, context.DeadlineExceeded) || len(runs.Nodes) != 1 || runs.Nodes[0].Status != StatusFailed {
		t.Fatalf("runs = %+v, err = %v", runs, err)
	}
	record, readErr := e.GetExecution(runs.ExecutionID)
	if readErr != nil || record.Status != "needs_recovery" {
		t.Fatalf("record = %+v, error = %v, want explicit recovery", record, readErr)
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

func TestApplySavedPlans_TimeoutRequiresExplicitRecovery(t *testing.T) {
	e, _ := savedTestEngine(t)
	path := string(e.Binary)
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script = bytes.Replace(script, []byte("  apply)"), []byte("  apply)\n trap 'exit 130' INT\n sleep 30\n"), 1)
	if err := os.WriteFile(path, script, 0700); err != nil {
		t.Fatal(err)
	}
	record, err := e.SavePlans(Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.ApplySavedPlans(record.ID, Options{AutoApprove: true, NodeTimeout: 500 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline", err)
	}
	record, err = e.GetExecution(record.ID)
	if err != nil || record.Status != "needs_recovery" || record.Nodes[0].Phase != "indeterminate" {
		t.Fatalf("record = %+v, error = %v, want explicit recovery", record, err)
	}
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err == nil {
		t.Fatal("uncertain saved mutation replayed")
	}
}
