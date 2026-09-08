//go:build !windows

package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// setSnapshotOutputSensitive reloads a real module edit so the regression exercises the metadata supplied by module inspection.
func setSnapshotOutputSensitive(t *testing.T, e *Engine, output string, sensitive bool) *Engine {
	t.Helper()
	path := filepath.Join(e.BaseDir, "module", "main.tf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture module: %v", err)
	}
	header := `output "` + output + `" {` + "\n"
	src := strings.Replace(string(data), header+"  sensitive = true\n", header, 1)
	if sensitive {
		src = strings.Replace(src, header, header+"  sensitive = true\n", 1)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("writing fixture module: %v", err)
	}
	reloaded, err := Load(e.BaseDir, e.Binary, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load after sensitivity edit: %v", err)
	}
	return reloaded
}

// assertSnapshotWithheld checks the persisted bytes without printing the fixture's secret on a regression.
func assertSnapshotWithheld(t *testing.T, e *Engine, node, output string) []byte {
	t.Helper()
	data, err := os.ReadFile(e.snapshotPath(node))
	if err != nil {
		t.Fatalf("reading snapshot: %v", err)
	}
	var file struct {
		Outputs  map[string]any `json:"outputs"`
		Withheld []string       `json:"withheld"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("decoding snapshot: %v", err)
	}
	if _, exists := file.Outputs[output]; exists {
		t.Fatal("snapshot persisted a withheld output value")
	}
	if !slices.Contains(file.Withheld, output) {
		t.Fatalf("snapshot does not record withheld port %q", output)
	}
	return data
}

// assertSnapshotRefused pins the consumer diagnostic, remedy, and original subprocess failure without exposing the withheld value.
func assertSnapshotRefused(t *testing.T, e *Engine) {
	t.Helper()
	_, err := e.Apply(Options{Node: "b", AutoApprove: true})
	if err == nil {
		t.Fatal("Apply reused a sensitive snapshot value, want a withheld-output error")
	}
	for _, text := range []string{"node.b.input.consumed", "node.a.output.consumed", "withheld", "sensitive", "restore", "live"} {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("Apply error omits %q", text)
		}
	}
	var exitErr *osexec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatal("withheld-output error does not wrap the live output failure")
	}
	if _, err := os.Stat(filepath.Join(e.dataDir("b"), "varfile-seen")); !os.IsNotExist(err) {
		t.Fatal("consumer plan ran despite the withheld output")
	}
}

func TestApply_SnapshotWithholdsSensitiveOutputOnChangedAndUnchangedApply(t *testing.T) {
	e, _ := loadSnapshotTestEngine(t, true)
	e = setSnapshotOutputSensitive(t, e, "consumed", true)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("changed Apply: %v", err)
	}
	first := assertSnapshotWithheld(t, e, "a", "consumed")
	if bytes.Contains(first, []byte(`"ok"`)) {
		t.Fatal("snapshot contains the sensitive fixture value outside outputs")
	}
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("unchanged Apply: %v", err)
	}
	second := assertSnapshotWithheld(t, e, "a", "consumed")
	if !bytes.Equal(first, second) {
		t.Fatal("withheld snapshot changed between identical applies")
	}
}

func TestApply_SnapshotRewriteRemovesNewlySensitiveValue(t *testing.T) {
	e, _ := loadSnapshotTestEngine(t, true)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("initial Apply: %v", err)
	}
	e = setSnapshotOutputSensitive(t, e, "consumed", true)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply after sensitivity edit: %v", err)
	}
	data := assertSnapshotWithheld(t, e, "a", "consumed")
	if bytes.Contains(data, []byte(`"ok"`)) {
		t.Fatal("rewritten snapshot retains a newly sensitive value")
	}
}

func TestResolveInputs_SensitiveLegacySnapshotIsRefused(t *testing.T) {
	e := setSnapshotOutputSensitive(t, loadFallbackEngine(t, true), "consumed", true)
	writeFallbackSnapshot(t, e, "a", "legacy-sensitive-fixture")
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
	assertSnapshotRefused(t, e)
}

func TestResolveInputs_PublishedWithheldSnapshotExplainsFailure(t *testing.T) {
	e := setSnapshotOutputSensitive(t, loadFallbackEngine(t, true), "consumed", true)
	if _, err := e.Apply(Options{Node: "a", AutoApprove: true}); err != nil {
		t.Fatalf("publishing Apply: %v", err)
	}
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
	assertSnapshotRefused(t, e)
}

func TestResolveInputs_SensitiveLiveOutputStillWorks(t *testing.T) {
	e := setSnapshotOutputSensitive(t, loadFallbackEngine(t, true), "consumed", true)
	writeFallbackSnapshot(t, e, "a", "stale-snapshot-fixture")
	t.Setenv("TG_OUTPUT_FIRST", "live-sensitive-fixture")
	if _, err := e.Apply(Options{Node: "b", AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(varfileSeen(t, e, "b"), "live-sensitive-fixture") {
		t.Fatal("consumer did not receive the live sensitive output")
	}
}

func TestResolveInputs_SensitiveAppliedOutputStillWorks(t *testing.T) {
	e := setSnapshotOutputSensitive(t, loadFallbackEngine(t, true), "consumed", true)
	t.Setenv("TG_OUTPUT_FIRST", "applied-sensitive-fixture")
	t.Setenv("TG_OUTPUT_LATER", "unexpected-reread-fixture")
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	seen := varfileSeen(t, e, "b")
	if !strings.Contains(seen, "applied-sensitive-fixture") || strings.Contains(seen, "unexpected-reread-fixture") {
		t.Fatal("consumer did not receive this run's applied sensitive output")
	}
	assertSnapshotWithheld(t, e, "a", "consumed")
}

func TestApply_SnapshotKeepsPublicOutputBesideSensitiveOutput(t *testing.T) {
	e, baseDir := loadSnapshotTestEngine(t, true)
	modulePath := filepath.Join(baseDir, "module", "main.tf")
	data, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatalf("reading module: %v", err)
	}
	data = append(data, []byte("variable \"internal\" { default = \"\" }\n")...)
	if err := os.WriteFile(modulePath, data, 0o644); err != nil {
		t.Fatalf("writing module: %v", err)
	}
	blueprintPath := filepath.Join(baseDir, "blueprint.hcl")
	bp, err := os.ReadFile(blueprintPath)
	if err != nil {
		t.Fatalf("reading blueprint: %v", err)
	}
	bp = append(bp, []byte("edge {\n  from = node.a.output.internal\n  to = node.b.input.internal\n}\n")...)
	writeBlueprint(t, baseDir, string(bp))
	e = setSnapshotOutputSensitive(t, e, "internal", true)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data = assertSnapshotWithheld(t, e, "a", "internal")
	if bytes.Contains(data, []byte(`"secret"`)) {
		t.Fatal("snapshot contains the sensitive fixture value")
	}
	var file struct {
		Outputs map[string]any `json:"outputs"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("decoding snapshot: %v", err)
	}
	if file.Outputs["consumed"] != "ok" || len(file.Outputs) != 1 {
		t.Fatal("snapshot did not retain exactly the consumed public output")
	}
}

func TestApply_SnapshotWithholdsUnknownSensitivity(t *testing.T) {
	e := loadFallbackEngine(t, true)
	// An incomplete schema is not produced by Inspect today, but must never authorize cleartext storage if one reaches the engine.
	e.Graph.Nodes["a"].Schema.OutputDetails = nil
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data := assertSnapshotWithheld(t, e, "a", "consumed")
	if bytes.Contains(data, []byte(`"first"`)) {
		t.Fatal("snapshot contains a value whose sensitivity is unknown")
	}
}

func TestResolveInputs_UnknownSensitivityLegacySnapshotIsRefused(t *testing.T) {
	e := loadFallbackEngine(t, true)
	writeFallbackSnapshot(t, e, "a", "unknown-sensitivity-fixture")
	e.Graph.Nodes["a"].Schema.OutputDetails = nil
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
	assertSnapshotRefused(t, e)
}

func TestResolveInputs_WithheldSnapshotNeedsRepublishingAfterSensitivityRemoval(t *testing.T) {
	e := setSnapshotOutputSensitive(t, loadFallbackEngine(t, true), "consumed", true)
	if _, err := e.Apply(Options{Node: "a", AutoApprove: true}); err != nil {
		t.Fatalf("publishing sensitive Apply: %v", err)
	}
	e = setSnapshotOutputSensitive(t, e, "consumed", false)
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
	assertSnapshotRefused(t, e)

	t.Setenv("TG_OUTPUT_FAIL_NODE", "")
	t.Setenv("TG_OUTPUT_LATER", "republished-public-fixture")
	if _, err := e.Apply(Options{Node: "a", AutoApprove: true}); err != nil {
		t.Fatalf("republishing non-sensitive Apply: %v", err)
	}
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
	if _, err := e.Apply(Options{Node: "b", AutoApprove: true}); err != nil {
		t.Fatalf("Apply after republishing: %v", err)
	}
	if !strings.Contains(varfileSeen(t, e, "b"), "republished-public-fixture") {
		t.Fatal("consumer did not receive the newly published public snapshot")
	}
}

func TestResolveInputs_UnusableSnapshotPreservesLiveErrorForSensitiveOutput(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "missing"},
		{name: "corrupt", data: `{"schema":1,"node":"a","outputs":{`},
		{name: "missing outputs", data: `{"schema":1,"node":"a"}`},
		{name: "null outputs", data: `{"schema":1,"node":"a","outputs":null}`},
		{name: "unknown schema", data: `{"schema":99,"node":"a","outputs":{}}`},
		{name: "wrong node", data: `{"schema":1,"node":"other","outputs":{}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := setSnapshotOutputSensitive(t, loadFallbackEngine(t, true), "consumed", true)
			if test.data != "" {
				writeFallbackSnapshot(t, e, "a", "placeholder")
				if err := os.WriteFile(e.snapshotPath("a"), []byte(test.data), 0o600); err != nil {
					t.Fatalf("writing unusable snapshot: %v", err)
				}
			}
			t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
			_, err := e.Apply(Options{Node: "b", AutoApprove: true})
			if err == nil || !strings.Contains(err.Error(), `reading existing outputs from upstream node "a" failed`) || strings.Contains(err.Error(), "withheld") {
				t.Fatal("unusable snapshot replaced the original live-read diagnostic")
			}
			var exitErr *osexec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatal("error does not wrap the live output failure")
			}
		})
	}
}

func TestApply_SensitiveSnapshotOptOutLeavesExistingFileUntouched(t *testing.T) {
	e := setSnapshotOutputSensitive(t, loadFallbackEngine(t, false), "consumed", true)
	writeFallbackSnapshot(t, e, "a", "legacy-sensitive-fixture")
	before, err := os.ReadFile(e.snapshotPath("a"))
	if err != nil {
		t.Fatalf("reading original snapshot: %v", err)
	}
	if _, err := e.Apply(Options{Node: "a", AutoApprove: true}); err != nil {
		t.Fatalf("opted-out Apply: %v", err)
	}
	after, err := os.ReadFile(e.snapshotPath("a"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("opted-out Apply changed the existing snapshot")
	}
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
	_, err = e.Apply(Options{Node: "b", AutoApprove: true})
	if err == nil || !strings.Contains(err.Error(), `reading existing outputs from upstream node "a" failed`) || strings.Contains(err.Error(), "withheld") {
		t.Fatal("opted-out Apply consulted a sensitive snapshot")
	}
}

func TestResolveInputs_StructurallyCorruptSnapshotPreservesLiveError(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "missing outputs", data: `{"schema":1,"node":"a"}`},
		{name: "null outputs", data: `{"schema":1,"node":"a","outputs":null}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := loadFallbackEngine(t, true)
			writeFallbackSnapshot(t, e, "a", "public-fixture")
			if err := os.WriteFile(e.snapshotPath("a"), []byte(test.data), 0o600); err != nil {
				t.Fatalf("writing structurally corrupt snapshot: %v", err)
			}
			t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
			_, err := e.Apply(Options{Node: "b", AutoApprove: true})
			var exitErr *osexec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatal("structurally corrupt snapshot discarded the live output error chain")
			}
			if !strings.Contains(err.Error(), `reading existing outputs from upstream node "a" failed`) {
				t.Fatal("structurally corrupt snapshot replaced the original live-read diagnostic")
			}
		})
	}
}
