//go:build !windows

package engine

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// installVersionedTofu records every command so a preflight cannot accidentally read outputs or write tfvars before refusing an incompatible runtime.
func installVersionedTofu(t *testing.T, dir, version string) string {
	t.Helper()
	script, err := os.ReadFile(writeFallbackFakeTerraform(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "runtime-calls")
	t.Setenv("TG_RUNTIME_CALLS", logPath)
	prefix := "#!/bin/sh\necho \"$1\" >> \"$TG_RUNTIME_CALLS\"\nif [ \"$1\" = version ]; then\n printf '%s' '{\"terraform_version\":\"" + version + "\"}'\n exit 0\nfi\n"
	script = bytes.Replace(script, []byte("#!/bin/sh\n"), []byte(prefix), 1)
	if err := os.WriteFile(filepath.Join(dir, "tofu"), script, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func TestRun_RejectsOldTofuBeforeSensitiveInputValidation(t *testing.T) {
	for _, command := range []string{"plan", "apply", "destroy"} {
		t.Run(command, func(t *testing.T) {
			dir := t.TempDir()
			writeFallbackModule(t, filepath.Join(dir, "module"))
			source := "terraform {\n backend \"local\" {}\n}\nvariable \"v\" {\n type = object({allowed = string})\n sensitive = true\n}\n"
			for name, src := range map[string]string{"main.tf": source, "main.tofu": strings.ReplaceAll(source, "true", "false")} {
				if err := os.WriteFile(filepath.Join(dir, "module", name), []byte(src), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path := writeBlueprint(t, dir, `node "a" {
 source = "./module"
 vars = { v = { allowed = { synthetic_sensitive_key = "fixture" } } }
}`)
			calls := installVersionedTofu(t, dir, "1.7.3")
			e, err := Load(path, exec.OpenTofu, io.Discard, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(calls); !os.IsNotExist(err) {
				t.Fatal("static Load executed runtime")
			}
			if problems := e.Validate(); len(problems) != 0 {
				t.Fatalf("static Validate: %v", problems)
			}
			if _, err := os.Stat(calls); !os.IsNotExist(err) {
				t.Fatal("static Validate executed runtime")
			}
			var runErr error
			switch command {
			case "plan":
				_, runErr = e.Plan(Options{})
			case "apply":
				_, runErr = e.Apply(Options{AutoApprove: true})
			case "destroy":
				_, runErr = e.Destroy(Options{AutoApprove: true})
			}
			if runErr == nil || !strings.Contains(runErr.Error(), "OpenTofu 1.8.0") {
				t.Fatalf("run error = %v, want file compatibility refusal", runErr)
			}
			if strings.Contains(runErr.Error(), "synthetic_sensitive_key") {
				t.Fatal("preflight leaked input details")
			}
			observed, err := os.ReadFile(calls)
			if err != nil || string(observed) != "version\n" {
				t.Fatalf("runtime commands = %q, err=%v, want only version", observed, err)
			}
			if _, err := os.Stat(e.tfVarsPath("a")); !os.IsNotExist(err) {
				t.Fatal("preflight wrote tfvars")
			}
		})
	}
}

func TestApply_RejectsOldTofuUpstreamBeforeSnapshotRead(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"upstream", "downstream"} {
		writeFallbackModule(t, filepath.Join(dir, name))
	}
	src, err := os.ReadFile(filepath.Join(dir, "upstream", "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "upstream", "main.tofu"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "upstream", "main.tf"), bytes.ReplaceAll(src, []byte(`value = "x"`), []byte("value = \"x\"\n sensitive = true")), 0o600); err != nil {
		t.Fatal(err)
	}
	path := writeBlueprint(t, dir, `snapshots {}
node "a" { source = "./upstream" }
node "b" { source = "./downstream" }
edge {
 from = node.a.output.consumed
 to = node.b.input.consumed
}`)
	calls := installVersionedTofu(t, dir, "1.7.3")
	t.Setenv("TG_OUTPUT_FAIL_NODE", "a")
	e, err := Load(path, exec.OpenTofu, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	writeFallbackSnapshot(t, e, "a", "synthetic-reclassified-output")
	original, err := os.ReadFile(e.snapshotPath("a"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Apply(Options{Node: "b", AutoApprove: true})
	if err == nil || !strings.Contains(err.Error(), "node.a.runtime") || !strings.Contains(err.Error(), "OpenTofu 1.8.0") {
		t.Fatalf("run error = %v, want upstream file compatibility refusal", err)
	}
	observed, err := os.ReadFile(calls)
	if err != nil || string(observed) != "version\n" {
		t.Fatalf("runtime commands = %q, err=%v, want no output read or execution", observed, err)
	}
	current, err := os.ReadFile(e.snapshotPath("a"))
	if err != nil || !bytes.Equal(original, current) {
		t.Fatal("preflight changed stored snapshot")
	}
}

func TestApply_OldTofuTFOnlyDoesNotProbeVersion(t *testing.T) {
	e := loadFallbackEngine(t, true)
	calls := installVersionedTofu(t, e.BaseDir, "1.7.3")
	e, err := Load(filepath.Join(e.BaseDir, "blueprint.hcl"), exec.OpenTofu, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(Options{Node: "a", AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	observed, err := os.ReadFile(calls)
	if err != nil || strings.Contains(string(observed), "version") {
		t.Fatalf("tf-only runtime commands = %q, err=%v", observed, err)
	}
}

func TestApply_UnselectedTofuSourceDoesNotProbeVersion(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"selected", "unselected"} {
		writeFallbackModule(t, filepath.Join(dir, name))
	}
	source, err := os.ReadFile(filepath.Join(dir, "unselected", "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unselected", "main.tofu"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	calls := installVersionedTofu(t, dir, "1.7.3")
	path := writeBlueprint(t, dir, `node "a" { source = "./selected" }
node "b" { source = "./unselected" }`)
	e, err := Load(path, exec.OpenTofu, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(Options{Node: "a", AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	observed, err := os.ReadFile(calls)
	if err != nil || strings.Contains(string(observed), "version") {
		t.Fatalf("unrelated runtime commands = %q, err=%v", observed, err)
	}
}
