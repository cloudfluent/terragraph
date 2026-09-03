//go:build !windows

package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func writeFixtureFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// writeObserveFixture builds a two-module graph (vpc -> app) plus a contracts.hcl covering one output and one input, over the standard fake terraform (whose `output` subcommand reports {"managed":{"value":"ok"}} once the node has a state, i.e. after an apply).
func writeObserveFixture(t *testing.T, contractsHCL string) (*Engine, string) {
	t.Helper()
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "modules", "vpc"))
	writeFixtureFile(t, filepath.Join(baseDir, "modules", "vpc", "outputs.tf"), `output "managed" { value = "x" }`)
	writeModule(t, filepath.Join(baseDir, "modules", "app"))
	writeFixtureFile(t, filepath.Join(baseDir, "modules", "app", "variables.tf"), "variable \"vpc_id\" {\n  type = string\n}\n")
	writeBlueprint(t, baseDir, `
node "vpc" { source = "./modules/vpc" }
node "app" { source = "./modules/app" }
edge {
  from = node.vpc.output.managed
  to   = node.app.input.vpc_id
}
`)
	if contractsHCL != "" {
		writeFixtureFile(t, filepath.Join(baseDir, "contracts.hcl"), contractsHCL)
	}
	e, err := Load(filepath.Join(baseDir, "blueprint.hcl"), exec.Binary(writeFakeTerraform(t, baseDir)), &strings.Builder{}, &strings.Builder{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return e, baseDir
}

func findEvidence(ev *Evidence, role, port string) PortEvidence {
	for _, p := range ev.Ports {
		if p.Role == role && p.Port == port {
			return p
		}
	}
	return PortEvidence{Confidence: "not-found"}
}

// TestObserve_ConfidenceBeforeAndAfterApply proves the three confidence levels in one graph: unapplied producer output is unknown, applied producer output is observed with a concrete type, and a consumer input with a .tf type constraint is declared.
func TestObserve_ConfidenceBeforeAndAfterApply(t *testing.T) {
	e, _ := writeObserveFixture(t, "")
	ev, err := e.Observe()
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	prod := findEvidence(ev, "producer", "managed")
	if prod.Confidence != "unknown" {
		t.Fatalf("got = %+v, want unknown before apply", prod)
	}
	cons := findEvidence(ev, "consumer", "vpc_id")
	if cons.Confidence != "declared" || cons.Type != "string" {
		t.Fatalf("got = %+v, want declared/string from the .tf type constraint", cons)
	}

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ev, err = e.Observe()
	if err != nil {
		t.Fatalf("Observe after apply: %v", err)
	}
	prod = findEvidence(ev, "producer", "managed")
	if prod.Confidence != "observed" || prod.Type != "string" {
		t.Fatalf("got = %+v, want observed/string after apply", prod)
	}
}

// TestObserve_InventoriesEveryPortAndBindsDigest proves the lock is a whole-graph inventory (contracted or not) carrying the contract set's digest, so a stale lock can never be mistaken for current evidence.
func TestObserve_InventoriesEveryPortAndBindsDigest(t *testing.T) {
	e, _ := writeObserveFixture(t, `
producer "./modules/vpc" {
  output "managed" { type = "string" }
}
`)
	ev, err := e.Observe()
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	// writeModule's main.tf gives each module an extra `id` output, so the real inventory is 4 ports: vpc{id,managed} producers, app{id} producer, app{vpc_id} consumer.
	if len(ev.Ports) != 4 {
		t.Fatalf("got = %d ports, want 4 (every port, not only contracted ones): %+v", len(ev.Ports), ev.Ports)
	}
	if p := findEvidence(ev, "producer", "managed"); p.Confidence != "unknown" || p.Scope != "./modules/vpc" {
		t.Fatalf("got = %+v, want the contracted producer present at its contracts.hcl scope, still unknown pre-apply", p)
	}
	if p := findEvidence(ev, "consumer", "vpc_id"); p.Confidence != "declared" || p.Scope != "./modules/app" {
		t.Fatalf("got = %+v, want the uncontracted consumer present at its blueprint-relative scope", p)
	}
	if ev.Schema != 1 || ev.ContractsDigest == "" || len(ev.ContractsDigest) != 64 {
		t.Fatalf("got schema=%d digest=%q, want 1 and a 64-char digest", ev.Schema, ev.ContractsDigest)
	}
}

// TestWriteLock_DeterministicBytesAndOwnerOnly proves two observes of the same reality write byte-identical files (so committed diffs mean reality changed, not map order) with owner-only permissions.
func TestWriteLock_DeterministicBytesAndOwnerOnly(t *testing.T) {
	e, baseDir := writeObserveFixture(t, "")
	ev, err := e.Observe()
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	first, err := e.WriteLock(ev)
	if err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	if first != filepath.Join(baseDir, LockFileName) {
		t.Fatalf("got = %q, want lock beside the blueprint", first)
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("reading lock: %v", err)
	}
	if len(a) == 0 || a[len(a)-1] != '\n' {
		t.Fatalf("lock must end with a trailing newline, got %q", a)
	}
	info, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat lock: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("got mode %v, want owner-only 0600", info.Mode().Perm())
	}
	ev2, err := e.Observe()
	if err != nil {
		t.Fatalf("Observe again: %v", err)
	}
	second, err := e.WriteLock(ev2)
	if err != nil {
		t.Fatalf("WriteLock again: %v", err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("reading lock again: %v", err)
	}
	if string(a) != string(b) {
		t.Fatal("two observes of the same graph wrote different bytes")
	}
	var roundTrip Evidence
	if err := json.Unmarshal(a, &roundTrip); err != nil {
		t.Fatalf("lock is not valid JSON: %v", err)
	}
}
