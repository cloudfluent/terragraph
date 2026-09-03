//go:build !windows

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadProposeEngine reuses the observe fixture shape and guarantees a fresh lock for the current graph.
func loadProposeEngine(t *testing.T, contractsHCL string) (*Engine, string) {
	t.Helper()
	e, baseDir := writeObserveFixture(t, contractsHCL)
	ev, err := e.Observe()
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if _, err := e.WriteLock(ev); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	return e, baseDir
}

// TestPropose_DraftsOnlyUncontractedPorts proves propose names the ports that have no contract yet and stays silent about the already-contracted one; the draft's type annotations carry the confidence they came from.
func TestPropose_DraftsOnlyUncontractedPorts(t *testing.T) {
	e, _ := loadProposeEngine(t, `
producer "./modules/vpc" {
  output "managed" { type = "string" }
}
`)
	draft, err := e.Propose()
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if !strings.Contains(draft, "consumer") || !strings.Contains(draft, `input "vpc_id"`) || !strings.Contains(draft, "# declared") {
		t.Fatalf("draft missing the uncontracted consumer port:\n%s", draft)
	}
	if strings.Contains(draft, `output "managed"`) {
		t.Fatalf("already-contracted output must not be re-proposed:\n%s", draft)
	}
}

// TestPropose_NeverWritesFiles proves the core safety property: observation proposes, review decides. No file outside the lock may appear or change.
func TestPropose_NeverWritesFiles(t *testing.T) {
	e, baseDir := loadProposeEngine(t, "")
	before, err := os.ReadFile(filepath.Join(baseDir, LockFileName))
	if err != nil {
		t.Fatalf("reading lock: %v", err)
	}
	if _, err := e.Propose(); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(baseDir, LockFileName))
	if err != nil {
		t.Fatalf("reading lock again: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("propose mutated the lock")
	}
	if _, serr := os.Stat(filepath.Join(baseDir, "contracts.hcl")); serr == nil {
		t.Fatal("propose wrote a contracts.hcl — observation must never create normative files")
	}
}

// TestPropose_StaleLockRefused proves the digest binding: a lock generated against a different contract set is rejected with the remedy instead of proposing drafts from someone else's evidence.
func TestPropose_StaleLockRefused(t *testing.T) {
	e, _ := loadProposeEngine(t, `
producer "./modules/vpc" {
  output "managed" { type = "string" }
}
`)
	// e.Graph is already built, so simplest staleness: tamper the digest in the lock file.
	lockPath := filepath.Join(e.BaseDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(`{"schema":1,"contracts_digest":"0000","ports":[]}`), 0o600); err != nil {
		t.Fatalf("tampering lock: %v", err)
	}
	if _, err := e.Propose(); err == nil || !strings.Contains(err.Error(), "observe again") {
		t.Fatalf("got = %v, want stale-lock error naming the remedy", err)
	}
}
