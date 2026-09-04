package exec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTFVars_WritesJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".terragraph.vpc.tfvars.json")

	got, err := WriteTFVars(path, map[string]any{"vpc_id": "vpc-123", "count": 3})
	if err != nil {
		t.Fatalf("WriteTFVars: %v", err)
	}
	if got != path {
		t.Fatalf("unexpected path: %s", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	var vals map[string]any
	if err := json.Unmarshal(data, &vals); err != nil {
		t.Fatalf("unmarshaling written file: %v", err)
	}
	if vals["vpc_id"] != "vpc-123" {
		t.Fatalf("unexpected vpc_id: %v", vals["vpc_id"])
	}
}

func TestWriteTFVars_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vars", "vpc.tfvars.json")

	if _, err := WriteTFVars(path, map[string]any{"a": 1}); err != nil {
		t.Fatalf("WriteTFVars: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist under a freshly created directory: %v", err)
	}
}

func TestWriteTFVars_EmptyRemovesStaleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".terragraph.vpc.tfvars.json")

	if _, err := WriteTFVars(path, map[string]any{"a": 1}); err != nil {
		t.Fatalf("WriteTFVars (seed): %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected seed file to exist: %v", err)
	}

	if _, err := WriteTFVars(path, map[string]any{}); err != nil {
		t.Fatalf("WriteTFVars (empty): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected stale tfvars file to be removed, stat err = %v", err)
	}
}

func TestVarFileArgs(t *testing.T) {
	if got := VarFileArgs("/x/vpc.tfvars.json", map[string]any{"a": 1}); len(got) != 1 || got[0] != "-var-file=/x/vpc.tfvars.json" {
		t.Fatalf("unexpected args for non-empty vars: %v", got)
	}
	if got := VarFileArgs("/x/vpc.tfvars.json", map[string]any{}); got != nil {
		t.Fatalf("expected nil args for empty vars, got %v", got)
	}
	if got := VarFileArgs("/x/vpc.tfvars.json", nil); got != nil {
		t.Fatalf("expected nil args for nil vars, got %v", got)
	}
}

func TestWriteTFVars_OwnerOnlyPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".terragraph.vpc.tfvars.json")

	if _, err := WriteTFVars(path, map[string]any{"vpc_id": "vpc-123"}); err != nil {
		t.Fatalf("WriteTFVars: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("tfvars perm = %o, want 0600 (resolved inputs may be secret outputs; the file is never shared)", got)
	}
}
