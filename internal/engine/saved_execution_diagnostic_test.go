package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func assertSavedOpenDiagnostic(t *testing.T, err error, id, code, category string) {
	t.Helper()
	diagnostics := Diagnostics(err, Diagnostic{})
	if len(diagnostics) != 1 || diagnostics[0].Code != code || diagnostics[0].Category != category || diagnostics[0].RelatedExecutionID != id {
		t.Fatalf("got = %+v, %v, want %s/%s for %s", diagnostics, err, code, category, id)
	}
}

func TestSavedExecution_MissingRecordIsReadFailure(t *testing.T) {
	for _, operation := range []string{"apply", "continue"} {
		t.Run(operation, func(t *testing.T) {
			e, _ := journalFixture(t)
			id := newExecutionID("run")
			var err error
			if operation == "apply" {
				_, err = e.ApplySavedPlans(id, Options{AutoApprove: true})
			} else {
				_, err = e.SavePlans(Options{}, id)
			}
			if !errors.Is(err, errExecutionMissing) {
				t.Fatalf("got = %v, want missing record", err)
			}
			assertSavedOpenDiagnostic(t, err, id, "execution_read_failed", "record")
		})
	}
}

func TestSavedExecution_StoreOpenFailureIsReadFailure(t *testing.T) {
	e, _ := journalFixture(t)
	if err := os.WriteFile(filepath.Join(e.BaseDir, ".terragraph", "executions"), []byte("blocked store directory"), 0600); err != nil {
		t.Fatal(err)
	}
	id := newExecutionID("run")
	_, err := e.ApplySavedPlans(id, Options{AutoApprove: true})
	assertSavedOpenDiagnostic(t, err, id, "execution_read_failed", "record")
}

func TestSavedExecution_CorruptRecordIsReadFailure(t *testing.T) {
	e, _ := journalFixture(t)
	s, err := e.beginExecution("saved_apply", []string{"example"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := s.record.ID
	s.close()
	if err := os.WriteFile(filepath.Join(e.BaseDir, ".terragraph", "executions", id+".json"), []byte("truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = e.ApplySavedPlans(id, Options{AutoApprove: true})
	assertSavedOpenDiagnostic(t, err, id, "execution_read_failed", "record")
}

func TestSavedExecution_UnsavedOperationIsIncompatible(t *testing.T) {
	e, _ := journalFixture(t)
	s, err := e.beginExecution("plan", []string{"example"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := s.record.ID
	s.close()
	_, err = e.ApplySavedPlans(id, Options{AutoApprove: true})
	assertSavedOpenDiagnostic(t, err, id, "saved_plan_incompatible", "artifact")
}

func TestSavedExecution_PeerRecoveryDiagnosticKeepsPeerID(t *testing.T) {
	e, _ := journalFixture(t)
	s, err := e.beginExecution("saved_apply", []string{"example"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := s.record.ID
	s.close()
	peer, err := e.beginExecution("apply", []string{"example"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.transition("example", "applying", "", ""); err != nil {
		peer.close()
		t.Fatal(err)
	}
	peerID := peer.record.ID
	peer.close()
	_, err = e.ApplySavedPlans(id, Options{AutoApprove: true})
	assertSavedOpenDiagnostic(t, err, peerID, "recovery_required", "recovery")
}
