//go:build !windows

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_SelectionJSONIncludesSuccessAndFailures(t *testing.T) {
	for _, command := range []string{"plan", "apply", "destroy"} {
		for _, fail := range []bool{false, true} {
			t.Run(command+map[bool]string{false: "_success", true: "_failure"}[fail], func(t *testing.T) {
				bp := writeSelectionRunFixture(t)

				if fail {
					t.Setenv("TG_FAKE_PLAN_FAIL", "1")
				}
				args := []string{command, "--node", "a", "--downstream", "--output", "json"}
				if command != "plan" {
					args = append(args, "--auto-approve")
				}
				out, _, err := runCmdAt(t, bp, args...)
				if (err != nil) != fail {
					t.Fatalf("got = %s, %v, want failure %t", out, err, fail)
				}
				var result runResult
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatalf("got = %q, %v", out, err)
				}
				if result.SchemaVersion != 1 || result.ExecutionID == "" || result.Selection == nil || result.Selection.Mode != "downstream" || len(result.Nodes) != 1 || len(result.Selection.Nodes) != 1 {
					t.Fatalf("got = %s", out)
				}
				if fail && len(result.Nodes[0].Diagnostics) == 0 {
					t.Fatalf("got = %s, want node diagnostics alongside selection and execution ID", out)
				}
			})
		}
	}
}

func TestDestroy_SelectionJSONSurvivesPreparationFailure(t *testing.T) {
	bp := writeSelectionRunFixture(t)
	path := filepath.Join(filepath.Dir(bp), "blueprint.hcl")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), "source = \"./module\"", "source = \"./module\"\n approve = \"none\"", 1))
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	out, _, err := runCmdAt(t, bp, "destroy", "--node", "a", "--auto-approve", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "does not permit teardown") {
		t.Fatalf("got = %v", err)
	}
	var result runResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("got = %q, %v", out, err)
	}
	if result.Selection == nil || len(result.Nodes) != 0 || result.ExecutionID != "" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "policy_blocked" {
		t.Fatalf("got = %s", out)
	}
}

func TestPlan_UnresolvedSelectionIsNotPublished(t *testing.T) {
	bp := writeSelectionRunFixture(t)
	out, _, err := runCmdAt(t, bp, "plan", "--node", "", "--output", "json")
	if err == nil {
		t.Fatal("empty name accepted")
	}
	var result planResultDTO
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Selection != nil || len(result.Nodes) != 0 || len(result.Diagnostics) != 1 {
		t.Fatalf("got = %s", out)
	}
}

func TestGraph_SelectionNeverCallsRuntimeOrCreatesRecords(t *testing.T) {
	bp := writeSelectionRunFixture(t)
	for _, format := range []string{"list", "dot"} {
		out, stderr, err := runCmdAt(t, bp, "graph", "--node", "a", "--downstream", "--format", format)
		if err != nil || strings.Contains(out+stderr, "terraform ") {
			t.Fatalf("got = %q, %q, %v", out, stderr, err)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(bp), ".terragraph")); !os.IsNotExist(err) {
		t.Fatalf("got = %v, graph created managed artifacts", err)
	}
}

func TestRun_SelectionTextPrecedesFirstSubprocess(t *testing.T) {
	bp := writeSelectionRunFixture(t)
	out, _, err := runCmdAt(t, bp, "apply", "--node", "a", "--auto-approve")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "selection: exact\nrequested: a\nselected: a\n") || strings.Index(out, "level 1: a") > strings.Index(out, "terraform init stdout") {
		t.Fatalf("got = %q", out)
	}
}

func writeSelectionRunFixture(t *testing.T) string {
	t.Helper()
	bp := writeRunFixture(t)
	fake := filepath.Join(filepath.Dir(bp), "terraform-fake")
	script, err := os.ReadFile(fake)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(script), `if [ "$1" != show ];`, `if [ "$1" != show ] && [ "$1" != output ];`, 1)
	updated = strings.Replace(updated, "case \"$1\" in", `case "$1" in
 output) printf '%s' '{"id":{"value":"existing"}}'; exit 0 ;;
 destroy) if [ -n "${TG_FAKE_PLAN_FAIL:-}" ]; then exit 1; fi; exit 0 ;;`, 1)
	if err := os.WriteFile(fake, []byte(updated), 0700); err != nil {
		t.Fatal(err)
	}
	return bp
}

func TestGraph_SelectionCannotHideInvalidUnselectedSchema(t *testing.T) {
	bp := writeSelectionRunFixture(t)
	module := filepath.Join(filepath.Dir(bp), "bad", "main.tf")
	writeFixtureFile(t, module, "output \"present\" { value = \"x\" }\n")
	body, err := os.ReadFile(bp)
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, []byte("node \"bad\" { source = \"./bad\" }\nedge {\n from = node.bad.output.missing\n to = node.a.input.missing\n}\n")...)
	if err := os.WriteFile(bp, body, 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = runCmdAt(t, bp, "graph", "--node", "a")
	if err == nil || !strings.Contains(err.Error(), "terragraph validate") {
		t.Fatalf("got = %v, want full validation failure", err)
	}
}
