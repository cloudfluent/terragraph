//go:build !windows

package engine

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestApply_RecordsCompletionWithoutBundle(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	records, err := e.ListExecutions()
	if err != nil || len(records) != 1 || records[0].Nodes[0].Phase != "completed" {
		t.Fatalf("got = %+v, %v", records, err)
	}
	store, err := e.openExecutionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.close() }()
	keys, err := store.list(e.context())
	if err != nil || len(keys) != 1 || !strings.HasPrefix(keys[0], "run-") {
		t.Fatalf("got = %v, %v", keys, err)
	}
}

func TestRecoverExecution_AppliedOnlyReadsOutputs(t *testing.T) {
	e, _, log := loadApplyTestEngine(t)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	names, err := e.executionLevels(Options{}, false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.beginExecution("apply", names[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := s.transition(names[0][0], "applied", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.finish(errors.New("output unavailable")); err != nil {
		t.Fatal(err)
	}
	id := s.record.ID
	s.close()
	before, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	record, err := e.RecoverExecution(id, true, false, false)
	if err != nil || record.Nodes[0].Phase != "completed" {
		t.Fatalf("got = %+v, %v", record, err)
	}
	after, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(before), "apply\n") != strings.Count(string(after), "apply\n") {
		t.Fatalf("recovery reapplied: %s", after)
	}
}

func TestRecoverExecution_UnknownRequiresInspectionAndPreservesOutcome(t *testing.T) {
	e, _ := journalFixture(t)
	s, err := e.beginExecution("apply", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.transition("example", "indeterminate", "", ""); err != nil {
		t.Fatal(err)
	}
	id := s.record.ID
	s.close()
	for _, options := range [][3]bool{{false, true, true}, {true, false, true}, {true, false, false}} {
		if _, err := e.RecoverExecution(id, options[0], options[1], options[2]); err == nil {
			t.Fatalf("accepted incomplete recovery: %v", options)
		}
	}
	record, err := e.RecoverExecution(id, true, true, true)
	if err != nil || record.Nodes[0].Phase != "indeterminate" || record.Status != "recovered_replan_required" {
		t.Fatalf("got = %+v, %v", record, err)
	}
	next, err := e.beginExecution("apply", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	next.close()
}
