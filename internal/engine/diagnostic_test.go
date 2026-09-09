package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestDiagnostics_JoinedCancellationAndRecordConflict(t *testing.T) {
	recordErr := WithDiagnostic(fmt.Errorf("write execution: %w", errExecutionConflict), Diagnostic{Code: "execution_record_write_failed", Category: "record", Phase: "record", RelatedExecutionID: "run-existing"})
	err := errors.Join(fmt.Errorf("node.a: %w", context.Canceled), recordErr, fmt.Errorf("wrapped: %w", recordErr))
	got := Diagnostics(err, Diagnostic{Code: "runtime_failed", Category: "runtime"})
	if len(got) != 2 || got[0].Code != "cancelled" || got[1].Code != "execution_record_conflict" || got[1].RelatedExecutionID != "run-existing" {
		t.Fatalf("got = %+v", got)
	}
	if !errors.Is(err, errExecutionConflict) || !errors.Is(err, context.Canceled) {
		t.Fatal("error identity lost")
	}
}

func TestDiagnostics_CancelledRecordWriteRemainsRecordFailure(t *testing.T) {
	err := WithDiagnostic(context.Canceled, Diagnostic{Code: "execution_record_write_failed", Category: "record"})
	got := Diagnostics(errors.Join(context.Canceled, err), Diagnostic{})
	if len(got) != 2 || got[1].Code != "execution_record_write_failed" {
		t.Fatalf("got = %+v", got)
	}
}

func TestExecutionJournal_BarrierDiagnosticReferencesPreviousRun(t *testing.T) {
	e, _ := journalFixture(t)
	s, err := e.beginExecution("apply", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.transition("example", "applying", "", ""); err != nil {
		t.Fatal(err)
	}
	result, err := e.Apply(Options{AutoApprove: true})
	got := Diagnostics(err, Diagnostic{})
	if result.ExecutionID != "" || len(got) != 1 || got[0].Code != "recovery_required" || got[0].RelatedExecutionID != s.record.ID {
		t.Fatalf("got = %+v, %+v, %v", result, got, err)
	}
}

func TestExecutionJournal_ConflictDoesNotReportUnpersistedStatus(t *testing.T) {
	e, _ := journalFixture(t)
	s, err := e.beginExecution("apply", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	old := s.record
	next := old
	next.Status = "completed"
	s.revision = "stale-revision"
	err = s.publish(next)
	got := Diagnostics(err, Diagnostic{})
	if !errors.Is(err, errExecutionConflict) || s.record.Status != old.Status || len(got) != 1 || got[0].RelatedExecutionID != old.ID || got[0].Code != "execution_record_conflict" {
		t.Fatalf("got = %+v, %+v, %v", s.record, got, err)
	}
}
