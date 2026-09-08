//go:build !windows

package engine

import (
	"github.com/cloudfluent/terragraph/internal/blueprint"
	"os"
	"strings"
	"testing"
	"time"
)

func savedTestEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	e, _, log := loadApplyTestEngine(t)
	path := string(e.Binary)
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(script), `case "$1" in`, `case "$1" in
version) printf '{"terraform_version":"1.5.7","platform":"fixture"}'; exit 0 ;;`, 1)
	updated = strings.Replace(updated, `'{"resource_changes":`, `'{"format_version":"1.0","resource_changes":`, 1)
	if err := os.WriteFile(path, []byte(updated), 0700); err != nil {
		t.Fatal(err)
	}
	return e, log
}

func TestSavedExecution_SavesWithoutApplyAndConsumesOnce(t *testing.T) {
	e, log := savedTestEngine(t)
	record, err := e.SavePlans(Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil || strings.Contains(string(data), "apply\n") {
		t.Fatalf("got = %s, %v", data, err)
	}
	if record.Nodes[0].Review == nil || !record.Nodes[0].Review.Evidence {
		t.Fatal("saved review missing")
	}
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err == nil {
		t.Fatal("replayed consumed execution")
	}
	data, err = os.ReadFile(log)
	if err != nil || strings.Count(string(data), "apply\n") != 1 || strings.Count(string(data), "plan\n") != 1 {
		t.Fatalf("got = %s, %v", data, err)
	}
}

func TestSavedExecution_BlocksOnAnotherUncertainExecution(t *testing.T) {
	e, log := savedTestEngine(t)
	record, err := e.SavePlans(Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.beginExecution("apply", []string{"cached"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.transition("cached", "indeterminate", "", ""); err != nil {
		t.Fatal(err)
	}
	s.close()
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err == nil {
		t.Fatal("uncertain peer did not block apply")
	}
	data, err := os.ReadFile(log)
	if err != nil || strings.Contains(string(data), "apply\n") {
		t.Fatalf("got = %s, %v", data, err)
	}
}

func TestSavedExecution_ExpiredPlanCannotApply(t *testing.T) {
	e, log := savedTestEngine(t)
	e.Blueprint.Execution = &blueprint.ExecutionConfig{PlanTTL: time.Nanosecond, RecordRetention: 30 * 24 * time.Hour}
	record, err := e.SavePlans(Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("got = %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil || strings.Contains(string(data), "apply\n") {
		t.Fatalf("got = %s, %v", data, err)
	}
}
