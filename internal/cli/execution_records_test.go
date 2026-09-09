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
	if err := os.WriteFile(filepath.Join(root, "run-"+strings.Repeat("b", 32)+".json"), []byte("truncated"), 0600); err != nil {
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
	list := NewRootCmd("test")
	out.Reset()
	list.SetOut(&out)
	list.SetErr(&bytes.Buffer{})
	list.SetArgs([]string{"--blueprint", path, "plan", "list", "--output", "json"})
	if err := list.Execute(); err == nil {
		t.Fatal("corrupt sibling was not diagnosed")
	}
	if !strings.Contains(out.String(), id) || !strings.Contains(out.String(), "execution_read_failed") {
		t.Fatalf("got = %s", out.String())
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

func TestPlanHistory_BackupExportIsExplicitAndRaw(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blueprint.hcl")
	if err := os.WriteFile(path, []byte(`node "example" { source = "./missing" }`), 0600); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(dir)
	digest := sha256.Sum256(encoded)
	id := "run-" + strings.Repeat("a", 32)
	record, _ := json.Marshal(map[string]any{"schema_version": 1, "id": id, "scope": hex.EncodeToString(digest[:]), "status": "completed", "operation": "run_state_rm", "backup": true, "nodes": []any{}})
	root := filepath.Join(dir, ".terragraph", "executions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for key, data := range map[string][]byte{id + ".json": record, id + ".bin": []byte("PRIVATE_NATIVE_BACKUP")} {
		object, _ := json.Marshal(map[string]any{"revision": "fixture", "data": data})
		if err := os.WriteFile(filepath.Join(root, key), object, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	show := NewRootCmd("test")
	show.SetOut(&out)
	show.SetErr(&bytes.Buffer{})
	show.SetArgs([]string{"--blueprint", path, "plan", "show", id, "--output", "json"})
	if err := show.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "PRIVATE_NATIVE_BACKUP") || !strings.Contains(out.String(), `"backup_available":true`) {
		t.Fatalf("got = %s", out.String())
	}
	out.Reset()
	plain := NewRootCmd("test")
	plain.SetOut(&out)
	plain.SetErr(&bytes.Buffer{})
	plain.SetArgs([]string{"--blueprint", path, "plan", "show", id})
	if err := plain.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "backup: available") || strings.Contains(out.String(), "PRIVATE_NATIVE_BACKUP") {
		t.Fatalf("got = %s", out.String())
	}
	out.Reset()
	export := NewRootCmd("test")
	export.SetOut(&out)
	export.SetErr(&bytes.Buffer{})
	export.SetArgs([]string{"--blueprint", path, "plan", "show", id, "--backup"})
	if err := export.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "PRIVATE_NATIVE_BACKUP" {
		t.Fatalf("got = %q", out.String())
	}
}
