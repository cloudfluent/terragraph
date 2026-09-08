//go:build !windows

package engine

import "testing"

// TestApply_ReportAppliedThenUnchanged proves the apply report distinguishes a node that made changes from one whose plan reported none.
func TestApply_ReportAppliedThenUnchanged(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)

	runs, err := e.Apply(Options{AutoApprove: true})
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if len(runs) != 1 || runs[0].Node != "cached" || runs[0].Level != 1 || runs[0].Status != StatusApplied || runs[0].Err != nil {
		t.Fatalf("got = %+v, want cached at level 1 as applied", runs)
	}

	runs, err = e.Apply(Options{AutoApprove: true})
	if err != nil {
		t.Fatalf("unchanged Apply: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != StatusUnchanged {
		t.Fatalf("got = %+v, want cached as unchanged", runs)
	}
}

// TestPlan_ReportPlanned proves a plan run records its success as planned, not applied.
func TestPlan_ReportPlanned(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("setup Apply: %v", err)
	}

	runs, err := e.Plan(Options{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(runs) != 1 || runs[0].Node != "cached" || runs[0].Status != StatusPlanned || runs[0].Err != nil {
		t.Fatalf("got = %+v, want cached as planned", runs)
	}
}

// TestDestroy_ReportDestroyed proves a destroy run records its success as destroyed.
func TestDestroy_ReportDestroyed(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("setup Apply: %v", err)
	}

	runs, err := e.Destroy(Options{AutoApprove: true})
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if len(runs) != 1 || runs[0].Node != "cached" || runs[0].Status != StatusDestroyed || runs[0].Err != nil {
		t.Fatalf("got = %+v, want cached as destroyed", runs)
	}
}
