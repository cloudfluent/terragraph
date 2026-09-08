package engine

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func journalFixture(t *testing.T) (*Engine, func()) {
	t.Helper()
	dir := t.TempDir()
	writeModule(t, dir)
	path := writeBlueprint(t, dir, `node "example" { source = "./" }`)
	e, unlock, err := LoadLocked(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
	return e, unlock
}

func TestExecutionJournal_BlocksNewRunAfterUncertainMutation(t *testing.T) {
	e, _ := journalFixture(t)
	session, err := e.beginExecution("apply", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.transition("example", "applying", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := session.finish(errors.New("runtime interrupted")); err != nil {
		t.Fatal(err)
	}
	id := session.record.ID
	session.close()
	if _, err := e.beginExecution("apply", []string{"example"}); err == nil || !strings.Contains(err.Error(), id) {
		t.Fatalf("got = %v", err)
	}
	records, err := e.ListExecutions()
	if err != nil || len(records) != 1 || records[0].Status != "needs_recovery" || records[0].FinishedAt != nil {
		t.Fatalf("got = %+v, %v", records, err)
	}
}

func TestExecutionJournal_PreservesAppliedWhenOutputReadFails(t *testing.T) {
	e, _ := journalFixture(t)
	s, err := e.beginExecution("apply", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.transition("example", "applying", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.transition("example", "applied", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.finish(errors.New("output read failed")); err != nil {
		t.Fatal(err)
	}
	s.close()
	records, err := e.ListExecutions()
	if err != nil || len(records) != 1 || records[0].Nodes[0].Phase != "applied" || records[0].Status != "needs_recovery" {
		t.Fatalf("got = %+v, %v", records, err)
	}
}

func TestExecutionJournal_FinishedRunDoesNotCreatePlanBundle(t *testing.T) {
	e, _ := journalFixture(t)
	s, err := e.beginExecution("apply", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.transition("example", "completed", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.finish(nil); err != nil {
		t.Fatal(err)
	}
	keys, err := s.store.list(context.Background())
	if err != nil || len(keys) != 1 || keys[0] != s.record.ID+".json" {
		t.Fatalf("got = %v, %v", keys, err)
	}
	s.close()
	next, err := e.beginExecution("apply", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	next.close()
}

func TestExecutionJournal_RecordsInterruptedResultAfterCancellation(t *testing.T) {
	e, _ := journalFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Context = ctx
	s, err := e.beginExecution("apply", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.transition("example", "applying", "", ""); err != nil {
		t.Fatal(err)
	}
	cancel()
	err = s.fail("example", "indeterminate", context.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got = %v", err)
	}
	if err := s.finish(err); err != nil {
		t.Fatal(err)
	}
	record, _, err := readExecutionRecord(context.Background(), s.store, s.record.ID)
	if err != nil || record.Status != "needs_recovery" {
		t.Fatalf("got = %+v, %v", record, err)
	}
}
