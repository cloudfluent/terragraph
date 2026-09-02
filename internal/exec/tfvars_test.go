package exec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTFVars_WritesJSON(t *testing.T) {
	dir := t.TempDir()

	path, err := WriteTFVars(dir, map[string]any{"vpc_id": "vpc-123", "count": 3})
	if err != nil {
		t.Fatalf("WriteTFVars: %v", err)
	}
	if path != filepath.Join(dir, TFVarsFileName) {
		t.Fatalf("unexpected path: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshaling written file: %v", err)
	}
	if got["vpc_id"] != "vpc-123" {
		t.Fatalf("unexpected vpc_id: %v", got["vpc_id"])
	}
}

func TestWriteTFVars_EmptyRemovesStaleFile(t *testing.T) {
	dir := t.TempDir()

	if _, err := WriteTFVars(dir, map[string]any{"a": 1}); err != nil {
		t.Fatalf("WriteTFVars (seed): %v", err)
	}
	path := filepath.Join(dir, TFVarsFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected seed file to exist: %v", err)
	}

	if _, err := WriteTFVars(dir, map[string]any{}); err != nil {
		t.Fatalf("WriteTFVars (empty): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected stale tfvars file to be removed, stat err = %v", err)
	}
}
