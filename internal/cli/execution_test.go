package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func executionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, filepath.Join(dir, "module", "main.tf"), "terraform {\n backend \"local\" {}\n}\nvariable \"value\" { default = \"\" }\noutput \"value\" { value = \"\" }")
	path := filepath.Join(dir, "blueprint.hcl")
	writeFixtureFile(t, path, fmt.Sprintf(`runtime "fake" {
 binary = %q
 default = true
}
node "a" { source = "./module" }
node "b" { source = "./module" }
node "c" { source = "./module" }
edge {
 from = node.a.output.value
 to = node.b.input.value
}
edge {
 from = node.b
 to = node.c
}
`, filepath.Join(dir, "must-not-start")))
	return path
}

func executeScopeCommand(t *testing.T, path string, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd("test")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"--blueprint", path}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func TestExecutionPreview_JSONNeverRunsOrLocks(t *testing.T) {
	for _, operation := range []string{"plan", "apply", "destroy"} {
		t.Run(operation, func(t *testing.T) {
			path := executionFixture(t)
			stdout, stderr, err := executeScopeCommand(t, path, operation, "--preview", "--node", "c", "--include-dependencies", "--output", "json", "--record-run", "--pool", "account=1:a,b")
			if err != nil {
				t.Fatalf("error = %v, stderr = %s", err, stderr)
			}
			var dto executionPreviewDTO
			if err := json.Unmarshal([]byte(stdout), &dto); err != nil {
				t.Fatalf("preview is not one JSON value: %v: %s", err, stdout)
			}
			if dto.Kind != "execution_scope" || dto.Operation != operation || len(dto.Nodes) != 3 {
				t.Fatalf("preview = %+v", dto)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".terragraph")); !os.IsNotExist(err) {
				t.Fatalf("preview wrote managed state: %v", err)
			}
		})
	}
}

func TestExecutionPreview_RepeatedTargetsAndExternalInputs(t *testing.T) {
	path := executionFixture(t)
	stdout, _, err := executeScopeCommand(t, path, "apply", "--preview", "--node", "b", "--node", "c", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var dto executionPreviewDTO
	if err := json.Unmarshal([]byte(stdout), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Nodes) != 2 || dto.Nodes[0].Node != "b" || len(dto.Nodes[0].ExternalInputs) != 1 || dto.Nodes[0].ExternalInputs[0].Node != "a" {
		t.Fatalf("preview = %+v", dto)
	}
}

func TestDestroy_RefusesExcludedDependentsBeforeRuntime(t *testing.T) {
	path := executionFixture(t)
	_, _, err := executeScopeCommand(t, path, "destroy", "--node", "a", "--auto-approve")
	if err == nil || !strings.Contains(err.Error(), "dependent nodes outside selection: b, c") {
		t.Fatalf("error = %v, want unsafe scope refusal before runtime lookup", err)
	}
	stdout, _, err := executeScopeCommand(t, path, "destroy", "--preview", "--node", "a")
	if err != nil || !strings.Contains(stdout, "outside dependents: b, c") {
		t.Fatalf("preview = %q, error = %v", stdout, err)
	}
}

func TestExecutionFlags_RejectInvalidLimitsBeforeLoading(t *testing.T) {
	for _, args := range [][]string{{"--node", ""}, {"--pool", "account=0:a"}, {"--timeout", "a=-1s"}, {"--output-retries", "11"}, {"--resume", "--node", "a"}} {
		_, _, err := executeScopeCommand(t, filepath.Join(t.TempDir(), "missing.hcl"), append([]string{"apply", "--auto-approve"}, args...)...)
		if err == nil || strings.Contains(err.Error(), "missing.hcl") {
			t.Fatalf("error = %v, want argument validation before loading", err)
		}
	}
}
