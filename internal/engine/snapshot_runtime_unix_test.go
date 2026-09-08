//go:build !windows

package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApply_SnapshotWithholdsRuntimeSensitiveOutput(t *testing.T) {
	e, dir := loadSnapshotTestEngine(t, true)
	path := filepath.Join(dir, "terraform-fake")
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script = bytes.ReplaceAll(script, []byte(`"consumed":{"sensitive":false,"value":"ok"}`), []byte(`"consumed":{"sensitive":true,"value":"ok"}`))
	if err := os.WriteFile(path, script, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"changed", "unchanged"} {
		if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
			t.Fatalf("%s Apply: %v", phase, err)
		}
		data := assertSnapshotWithheld(t, e, "a", "consumed")
		if strings.Contains(string(data), `"ok"`) {
			t.Fatal("runtime sensitive value persisted in snapshot")
		}
	}
}

func TestApply_LegacySnapshotNeedsRuntimeVerifiedRepublishing(t *testing.T) {
	e := loadFallbackEngine(t, true)
	path := filepath.Join(e.BaseDir, ".terragraph", "outputs", "a.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"schema":1,"node":"a","outputs":{"consumed":"legacy-secret"}}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
	assertSnapshotRefused(t, e)
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, legacy) {
		t.Fatal("reading a legacy snapshot changed its file")
	}
	t.Setenv("TG_OUTPUT_FAIL_NODE", "")
	if _, err := e.Apply(Options{Node: "a", AutoApprove: true}); err != nil {
		t.Fatalf("republish: %v", err)
	}
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
	if _, err := e.Apply(Options{Node: "b", AutoApprove: true}); err != nil {
		t.Fatalf("fallback after republishing: %v", err)
	}
	if !strings.Contains(varfileSeen(t, e, "b"), "first") {
		t.Fatal("republished public output was not reused")
	}
}

func TestApply_SnapshotWithholdsMissingAndNullRuntimeSensitivity(t *testing.T) {
	for _, metadata := range []string{"missing", "null"} {
		t.Run(metadata, func(t *testing.T) {
			e, dir := loadSnapshotTestEngine(t, true)
			path := filepath.Join(dir, "terraform-fake")
			script, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			replacement := `"consumed":{"value":`
			if metadata == "null" {
				replacement = `"consumed":{"sensitive":null,"value":`
			}
			script = bytes.ReplaceAll(script, []byte(`"consumed":{"sensitive":false,"value":`), []byte(replacement))
			if err := os.WriteFile(path, script, 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			assertSnapshotWithheld(t, e, "a", "consumed")
		})
	}
}

func TestApply_RuntimeSensitiveValuesFlowLiveButNotThroughSnapshot(t *testing.T) {
	e := loadFallbackEngine(t, true)
	path := filepath.Join(e.BaseDir, "terraform-fake")
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script = bytes.ReplaceAll(script, []byte(`"sensitive":false`), []byte(`"sensitive":true`))
	if err := os.WriteFile(path, script, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_OUTPUT_FIRST", "sensitive-live-fixture")
	if _, err := e.Apply(Options{AutoApprove: true, Parallelism: 2}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(varfileSeen(t, e, "b"), "sensitive-live-fixture") {
		t.Fatal("current run did not deliver sensitive output")
	}
	assertSnapshotWithheld(t, e, "a", "consumed")
	if err := os.Remove(filepath.Join(e.dataDir("b"), "varfile-seen")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
	assertSnapshotRefused(t, e)
}

func TestApply_SnapshotRewriteRemovesRuntimeSensitiveValue(t *testing.T) {
	e := loadFallbackEngine(t, true)
	if _, err := e.Apply(Options{Node: "a", AutoApprove: true}); err != nil {
		t.Fatalf("initial Apply: %v", err)
	}
	path := filepath.Join(e.BaseDir, "terraform-fake")
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script = bytes.ReplaceAll(script, []byte(`"sensitive":false`), []byte(`"sensitive":true`))
	if err := os.WriteFile(path, script, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(Options{Node: "a", AutoApprove: true}); err != nil {
		t.Fatalf("sensitive Apply: %v", err)
	}
	assertSnapshotWithheld(t, e, "a", "consumed")
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
	assertSnapshotRefused(t, e)
}
