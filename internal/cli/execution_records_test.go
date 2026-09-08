package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanHistory_ShowsRecordDespiteMissingModule(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	if err := os.WriteFile(path, []byte(`node "example" { source = "./missing" }`), 0600); err != nil {
		t.Fatal(err)
	}
	encodedDir, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encodedDir)
	id := "run-" + strings.Repeat("a", 32)
	record := map[string]any{"schema_version": 1, "id": id, "scope": hex.EncodeToString(digest[:]), "operation": "apply", "status": "needs_recovery", "nodes": []any{map[string]any{"name": "example", "phase": "applied", "target": "CANARY_PRIVATE_TARGET", "code": "output_read_failed"}}}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	object, err := json.Marshal(map[string]any{"revision": "fixture", "data": data})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, ".terragraph", "executions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, id+".json"), object, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd("test")
	var out, diagnostics bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&diagnostics)
	cmd.SetArgs([]string{"--blueprint", path, "plan", "show", id, "--output", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"phase":"applied"`) || strings.Contains(out.String(), "CANARY") || diagnostics.Len() != 0 {
		t.Fatalf("got = %s, diagnostics %s", out.String(), diagnostics.String())
	}
}

func TestPlanHistory_MissingRecordReturnsStructuredError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	if err := os.WriteFile(path, []byte(`node "example" { source = "./missing" }`), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--blueprint", path, "plan", "show", "run-" + strings.Repeat("a", 32), "--output", "json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("missing record succeeded")
	}
	if !strings.Contains(out.String(), `"code":"execution_read_failed"`) || !strings.Contains(out.String(), `"executions":[]`) {
		t.Fatalf("got = %s", out.String())
	}
}
