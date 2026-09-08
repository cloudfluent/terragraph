package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExecutionCleanup_PreservesOldUnknownRecordsAndBundles(t *testing.T) {
	e, _ := journalFixture(t)
	s, err := e.beginExecution("apply", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	planID := newExecutionID("plan")
	if _, err := s.store.write(e.context(), planID+".bin", []byte("private evidence"), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.transition("example", "indeterminate", "", planID); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-365 * 24 * time.Hour)
	next := s.record
	next.CreatedAt, next.UpdatedAt, next.FinishedAt = old, old, &old
	if err := s.publish(next); err != nil {
		t.Fatal(err)
	}
	id := s.record.ID
	s.close()
	if removed, err := e.PruneExecutions(); err != nil || len(removed) != 0 {
		t.Fatalf("got = %v, %v", removed, err)
	}
	if _, err := e.GetExecution(id); err != nil {
		t.Fatal(err)
	}
	store, err := e.openExecutionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.close() }()
	if _, err := store.read(e.context(), planID+".bin"); err != nil {
		t.Fatal("unknown bundle was removed")
	}
}

func TestExecutionCleanup_ExpiresPausedPlansBeforeDeletingRecords(t *testing.T) {
	e, _ := journalFixture(t)
	s, err := e.beginExecution("saved_apply", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	id, planID := s.record.ID, newExecutionID("plan")
	bundle := planBundle{SchemaVersion: 1, ID: planID, ExecutionID: id, Node: "example", ExpiresAt: time.Now().UTC().Add(-time.Hour), Plan: []byte("plan")}
	bytes, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.write(e.context(), planID+".bin", bytes, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.transition("example", "planned", "", planID); err != nil {
		t.Fatal(err)
	}
	next := s.record
	next.Status = "waiting_for_apply"
	if err := s.publish(next); err != nil {
		t.Fatal(err)
	}
	s.close()
	if removed, err := e.PruneExecutions(); err != nil || len(removed) != 0 {
		t.Fatalf("got = %v, %v", removed, err)
	}
	record, err := e.GetExecution(id)
	if err != nil || record.Status != "expired" || record.FinishedAt == nil {
		t.Fatalf("got = %+v, %v", record, err)
	}
	store, err := e.openExecutionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.close() }()
	if _, err := store.read(e.context(), planID+".bin"); !errors.Is(err, errExecutionMissing) {
		t.Fatalf("got = %v", err)
	}
}

func TestExecutionCleanup_RemovesBackupOnlyWithExpiredResolvedRecord(t *testing.T) {
	e, _ := journalFixture(t)
	s, err := e.beginExecution("run_state_rm", []string{"example"})
	if err != nil {
		t.Fatal(err)
	}
	id := s.record.ID
	if _, err := s.store.write(e.context(), id+".bin", []byte("native backup"), ""); err != nil {
		t.Fatal(err)
	}
	if err := s.transition("example", "completed", "", ""); err != nil {
		t.Fatal(err)
	}
	next := s.record
	next.Backup = true
	old := time.Now().UTC().Add(-365 * 24 * time.Hour)
	next.FinishedAt = &old
	next.Status = "completed"
	if err := s.publish(next); err != nil {
		t.Fatal(err)
	}
	s.close()
	scratch := filepath.Join(e.BaseDir, ".terragraph", "backups", id+".tfstate")
	if err := os.MkdirAll(filepath.Dir(scratch), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scratch, []byte("native backup"), 0600); err != nil {
		t.Fatal(err)
	}
	removed, err := e.PruneExecutions()
	if err != nil || len(removed) != 1 || removed[0] != id {
		t.Fatalf("got = %v, %v", removed, err)
	}
	store, err := e.openExecutionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.close() }()
	if _, err := store.read(e.context(), id+".bin"); !errors.Is(err, errExecutionMissing) {
		t.Fatalf("got = %v", err)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("got = %v", err)
	}
}
