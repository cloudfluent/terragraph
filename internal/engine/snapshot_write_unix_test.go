//go:build !windows

package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// writeSnapshotModule writes a module declaring two outputs — one an edge
// consumes, one nothing does — so the filtering rule has something real to
// filter. The shared-source two-node fixture needs the explicit local backend
// (two nodes instantiating one module without it is a graph validation error;
// with it, Build gives each node its own state path).
func writeSnapshotModule(t *testing.T, dir string) {
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
output "internal" {
  value = "y"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing fixture module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.txt"), []byte("version-one\n"), 0o644); err != nil {
		t.Fatalf("writing payload: %v", err)
	}
}

// writeSnapshotFakeTerraform is writeFakeTerraform trimmed to the apply flow,
// with `output` reporting two outputs instead of one: "consumed" (fed to node b
// by the fixture's edge) and "internal" (declared, published by terraform, read
// by no edge).
func writeSnapshotFakeTerraform(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "terraform-fake")
	script := `#!/bin/sh
case "$1" in
  init)
    exit 0
    ;;
  plan)
    planout=''
    for arg in "$@"; do
      case "$arg" in -out=*) planout="${arg#-out=}" ;; esac
    done
    if [ -f managed.out ] && cmp -s payload.txt managed.out; then
      exit 0
    fi
    if [ -n "$planout" ]; then
      cp payload.txt "$planout"
    fi
    exit 2
    ;;
  apply)
    # Real terraform refuses "output" until the node has a state; the marker
    # models that line between applied and never-applied.
    touch terraform.tfstate
    planfile=''
    for arg in "$@"; do
      case "$arg" in -*|apply) ;; *) planfile="$arg" ;; esac
    done
    cp "$planfile" managed.out
    exit 0
    ;;
  show)
    printf '{"resource_changes":[{"address":"fake.managed","change":{"actions":["create"]}}]}'
    exit 0
    ;;
  output)
    if [ ! -f terraform.tfstate ]; then
      printf 'Error: no state. Terraform has not been applied here.\n' >&2
      exit 1
    fi
    printf '{"consumed":{"value":"ok"},"internal":{"value":"secret"}}'
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

// loadSnapshotTestEngine loads the two-node fixture: a's "consumed" output
// feeds b's input, a's "internal" output feeds nothing. snapshots toggles the
// blueprint's opt-in block; everything else about the graph is identical.
func loadSnapshotTestEngine(t *testing.T, snapshots bool) (*Engine, string) {
	t.Helper()
	baseDir := t.TempDir()
	writeSnapshotModule(t, filepath.Join(baseDir, "module"))
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
	e, err := Load(writeBlueprint(t, baseDir, bp), exec.Binary(writeSnapshotFakeTerraform(t, baseDir)), &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return e, baseDir
}

func TestApply_SnapshotsOptOutWritesNothing(t *testing.T) {
	e, _ := loadSnapshotTestEngine(t, false)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// The gate is the blueprint block, not the graph's shape: this same graph
	// with `snapshots { }` does write, so opting out must leave no trace.
	if _, err := os.Stat(filepath.Join(e.BaseDir, ".terragraph", "outputs")); !os.IsNotExist(err) {
		t.Fatalf("opted-out graph wrote under .terragraph/outputs (stat err = %v), want nothing", err)
	}
}

func TestApply_SnapshotsWriteConsumedOutputsOnly(t *testing.T) {
	e, _ := loadSnapshotTestEngine(t, true)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("fresh Apply: %v", err)
	}

	// Both apply paths must leave the same artifact behind, so read the file
	// after the changed-node apply and again after an unchanged one.
	path := e.snapshotPath("a")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading snapshot after fresh apply: %v", err)
	}

	var snap struct {
		Schema  int            `json:"schema"`
		Node    string         `json:"node"`
		Outputs map[string]any `json:"outputs"`
	}
	if err := json.Unmarshal(first, &snap); err != nil {
		t.Fatalf("parsing snapshot %s: %v\n%s", path, err, first)
	}
	if snap.Schema != 1 || snap.Node != "a" {
		t.Fatalf("schema/node = %d/%q, want 1/%q", snap.Schema, snap.Node, "a")
	}
	if got, ok := snap.Outputs["consumed"]; !ok || got != "ok" {
		t.Fatalf("snapshot outputs[consumed] = %v (present=%t), want \"ok\"", got, ok)
	}
	if _, ok := snap.Outputs["internal"]; ok {
		t.Fatalf("snapshot contains unconsumed output \"internal\"; a value no edge reads must not be published: %v", snap.Outputs)
	}
	if len(snap.Outputs) != 1 {
		t.Fatalf("snapshot outputs = %v, want exactly the one consumed output", snap.Outputs)
	}
	if first[len(first)-1] != '\n' {
		t.Fatalf("snapshot must end with a newline: %q", first)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat snapshot: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("snapshot mode = %o, want 600", perm)
	}

	// b has no downstream consumer, so it publishes nothing.
	if _, err := os.Stat(e.snapshotPath("b")); !os.IsNotExist(err) {
		t.Fatalf("node b has no consumers, want no snapshot (stat err = %v)", err)
	}

	// Re-apply over an unchanged graph: the unchanged branch must write too,
	// byte-identically — nothing about the file may reveal which path wrote it.
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("unchanged Apply: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading snapshot after unchanged apply: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("snapshot changed between applies:\nfirst:  %q\nsecond: %q", first, second)
	}
}

// TestApply_SnapshotRewriteTightensLooseMode proves the remove-before-write rule: os.WriteFile only applies 0o600 on create, so a rewrite over a wider mode must start from a fresh file or the loose bits live on for the file's lifetime.
func TestApply_SnapshotRewriteTightensLooseMode(t *testing.T) {
	e, baseDir := loadSnapshotTestEngine(t, true)
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	path := filepath.Join(baseDir, ".terragraph", "outputs", "a.json")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("loosening mode: %v", err)
	}
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("rewrite Apply: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after rewrite: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("got mode %v after rewrite, want 0600 — a rewrite over loose bits kept them", info.Mode().Perm())
	}
}

// TestApply_SnapshotOrphanRemovedWhenConsumersVanish proves "no consumers → no file" holds on re-apply: removing the edge that justified a snapshot must remove the file, or a stale secret outlives its readers.
func TestApply_SnapshotOrphanRemovedWhenConsumersVanish(t *testing.T) {
	e, baseDir := loadSnapshotTestEngine(t, true)
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply with edge: %v", err)
	}
	path := filepath.Join(baseDir, ".terragraph", "outputs", "a.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot missing after edge-bearing apply: %v", err)
	}

	e2, err := Load(writeBlueprint(t, baseDir, "snapshots { }\nnode \"a\" { source = \"./module\" }\nnode \"b\" { source = \"./module\" }\n"), e.Binary, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load without edge: %v", err)
	}
	if err := e2.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply without edge: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("got %v, want the snapshot gone once no edge consumes it", err)
	}
}
