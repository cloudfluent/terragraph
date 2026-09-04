//go:build !windows

package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// writeFallbackModule writes the two-node fixture's shared module: one
// variable an edge can feed, one output it can read. The explicit local
// backend is required when two nodes share a source directory (Build then
// gives each its own state path).
func writeFallbackModule(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	src := `terraform {
  backend "local" {}
}
variable "consumed" {
  default = ""
}
output "consumed" {
  value = "x"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing fixture module: %v", err)
	}
}

// writeFallbackFakeTerraform is a fake terraform whose `output` subcommand the
// tests control through two env knobs, both inherited by the subprocess:
//
//   - TG_OUTPUT_FAIL_NODE: `output` exits 1 when run with the data dir of that
//     node, modelling a live read that fails (state gone, backend unreachable).
//   - TG_OUTPUT_FIRST / TG_OUTPUT_LATER: the value the first and every later
//     `output` call returns for a given node, so a test can tell the read that
//     populated the applied map from a re-read the engine should never make.
//
// Each node's `plan` copies the -var-file it was handed into its own data dir,
// which is the observable for which source won input resolution: the tfvars
// file is deleted after the run, but the copy survives.
func writeFallbackFakeTerraform(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "terraform-fake")
	script := `#!/bin/sh
case "$1" in
  init)
    mkdir -p "$TF_DATA_DIR"
    exit 0
    ;;
  plan)
    varfile=''
    for arg in "$@"; do
      case "$arg" in -var-file=*) varfile="${arg#-var-file=}" ;; esac
    done
    if [ -n "$varfile" ]; then
      cp "$varfile" "$TF_DATA_DIR/varfile-seen"
    fi
    planout=''
    for arg in "$@"; do
      case "$arg" in -out=*) planout="${arg#-out=}" ;; esac
    done
    if [ -n "$planout" ]; then
      printf 'plan' > "$planout"
    fi
    exit 2
    ;;
  apply)
    exit 0
    ;;
  show)
    printf '{"resource_changes":[{"address":"fake.managed","change":{"actions":["create"]}}]}'
    exit 0
    ;;
  output)
    if [ "$(basename "${TF_DATA_DIR:-none}")" = "${TG_OUTPUT_FAIL_NODE:-}" ]; then
      printf 'Error: no state. Terraform has not been applied here.\n' >&2
      exit 1
    fi
    mkdir -p "$TF_DATA_DIR"
    n=0
    if [ -f "$TF_DATA_DIR/output-calls" ]; then
      n=$(cat "$TF_DATA_DIR/output-calls")
    fi
    echo $((n+1)) > "$TF_DATA_DIR/output-calls"
    if [ "$n" -eq 0 ]; then
      printf '{"consumed":{"value":"%s"}}' "${TG_OUTPUT_FIRST:-first}"
    else
      printf '{"consumed":{"value":"%s"}}' "${TG_OUTPUT_LATER:-second}"
    fi
    exit 0
    ;;
esac
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake terraform: %v", err)
	}
	return path
}

// loadFallbackEngine loads the two-node fixture: a's "consumed" output feeds
// b's "consumed" input. snapshots toggles the blueprint's opt-in block; the
// graph is otherwise identical, so only the gate differs between the two.
func loadFallbackEngine(t *testing.T, snapshots bool) *Engine {
	t.Helper()
	baseDir := t.TempDir()
	writeFallbackModule(t, filepath.Join(baseDir, "module"))
	bp := `
node "a" { source = "./module" }
node "b" { source = "./module" }

edge {
  from = node.a.output.consumed
  to   = node.b.input.consumed
}
`
	if snapshots {
		bp = "snapshots { }\n" + bp
	}
	e, err := Load(writeBlueprint(t, baseDir, bp), exec.Binary(writeFallbackFakeTerraform(t, baseDir)), &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return e
}

// writeFallbackSnapshot hand-writes name's snapshot in the exact on-disk
// format writeSnapshot produces (T2), as a prior run's apply would have left
// behind.
func writeFallbackSnapshot(t *testing.T, e *Engine, name, value string) {
	t.Helper()
	path := e.snapshotPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating snapshot dir: %v", err)
	}
	body := `{"schema":1,"node":"` + name + `","outputs":{"consumed":"` + value + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing snapshot: %v", err)
	}
}

// varfileSeen returns what the fake terraform recorded as node's resolved
// inputs, or fails the test if the node's plan never ran.
func varfileSeen(t *testing.T, e *Engine, node string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.dataDir(node), "varfile-seen"))
	if err != nil {
		t.Fatalf("reading %s's recorded var file: %v", node, err)
	}
	return string(data)
}

// (a) opted in + live output fails + snapshot present: resolution succeeds
// from the snapshot and the apply proceeds. The upstream node is never applied
// in this run (node-scoped), so its outputs can only come from the live read
// or the file — this is the fallback actually firing.
func TestResolveInputs_SnapshotFallsBackWhenLiveOutputFails(t *testing.T) {
	e := loadFallbackEngine(t, true)
	writeFallbackSnapshot(t, e, "a", "snap")
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")

	// Applies only b: a is upstream, standing, and unreadable live.
	if err := e.Apply(Options{Node: "b", AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if seen := varfileSeen(t, e, "b"); !strings.Contains(seen, "snap") {
		t.Fatalf("b's resolved inputs = %q, want the snapshot value \"snap\"", seen)
	}
}

// (b) live output succeeds + a stale snapshot is sitting there: the live value
// wins. The snapshot must never preempt the live read, or the removed
// incremental-apply cache returns under a new name.
func TestResolveInputs_LiveOutputBeatsStaleSnapshot(t *testing.T) {
	e := loadFallbackEngine(t, true)
	writeFallbackSnapshot(t, e, "a", "stale-snap")
	t.Setenv("TG_OUTPUT_FIRST", "live")

	if err := e.Apply(Options{Node: "b", AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	seen := varfileSeen(t, e, "b")
	if !strings.Contains(seen, "live") {
		t.Fatalf("b's resolved inputs = %q, want the live value \"live\"", seen)
	}
	if strings.Contains(seen, "stale-snap") {
		t.Fatalf("b's resolved inputs = %q, stale snapshot value leaked into resolution", seen)
	}
}

// (c) applied map populated earlier in the same run: b resolves a's output from
// what a's apply just produced, never re-reading it live. TG_OUTPUT_LATER
// makes a live re-read return a different value, so the assertion can tell the
// two apart; combined with (b) (live beats snapshot) this pins the whole order
// applied > live > snapshot.
func TestResolveInputs_AppliedMapBeatsLiveReread(t *testing.T) {
	e := loadFallbackEngine(t, true)
	t.Setenv("TG_OUTPUT_FIRST", "applied-value")
	t.Setenv("TG_OUTPUT_LATER", "live-reread")

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	seen := varfileSeen(t, e, "b")
	if !strings.Contains(seen, "applied-value") {
		t.Fatalf("b's resolved inputs = %q, want the value captured in this run's applied map", seen)
	}
	if strings.Contains(seen, "live-reread") {
		t.Fatalf("b's resolved inputs = %q, a live re-read of a happened after a was already applied", seen)
	}
}

// (d) opted out + live fails + a snapshot file exists anyway: still the
// original failure. The gate is the blueprint block, not the file's existence.
func TestResolveInputs_OptOutFailsDespiteSnapshotFile(t *testing.T) {
	e := loadFallbackEngine(t, false)
	writeFallbackSnapshot(t, e, "a", "snap")
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")

	err := e.Apply(Options{Node: "b", AutoApprove: true})
	if err == nil {
		t.Fatal("Apply succeeded with an opted-out graph and a failing live read, want the original resolution error")
	}
	if !strings.Contains(err.Error(), `upstream node "a" has not been applied yet`) {
		t.Fatalf("Apply error = %q, want the original upstream-not-applied failure", err)
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("Apply error = %q, want it wrapping the live read's own error", err)
	}
}

// (e) opted in + live fails + the snapshot file is corrupt: the corrupt file
// is ignored (debug log), and the failure is still the original one. A
// fallback that errored on its own misses would be a second source of truth.
func TestResolveInputs_CorruptSnapshotIsNotAnError(t *testing.T) {
	e := loadFallbackEngine(t, true)
	path := e.snapshotPath("a")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating snapshot dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"schema":1,"node":"a","outputs":{`), 0o600); err != nil {
		t.Fatalf("writing corrupt snapshot: %v", err)
	}
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")

	err := e.Apply(Options{Node: "b", AutoApprove: true})
	if err == nil {
		t.Fatal("Apply succeeded on a corrupt snapshot and a failing live read, want the original resolution error")
	}
	if !strings.Contains(err.Error(), `upstream node "a" has not been applied yet`) {
		t.Fatalf("Apply error = %q, want the original upstream-not-applied failure, not a parse error", err)
	}
}
