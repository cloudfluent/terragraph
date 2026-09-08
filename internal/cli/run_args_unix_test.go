//go:build !windows

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCommands_RejectPositionalTargetsBeforeExecution(t *testing.T) {
	for _, command := range []string{"plan", "apply", "destroy"} {
		t.Run(command, func(t *testing.T) {
			bp := writeRunFixture(t)
			args := []string{command, "a", "--output", "json"}
			if command != "plan" {
				args = append(args, "--auto-approve")
			}
			stdout, stderr, err := runCmdAt(t, bp, args...)
			if err == nil || !strings.Contains(err.Error(), "--node") || !strings.Contains(err.Error(), "a") {
				t.Fatalf("error = %v, want unsupported target with --node remedy", err)
			}
			if stdout != "" || stderr != "" {
				t.Fatalf("stdout/stderr = %q/%q, want no execution output", stdout, stderr)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(bp), ".terragraph", "lock")); !os.IsNotExist(err) {
				t.Fatalf("lock stat = %v, want no execution lock created", err)
			}
		})
	}
}

func TestRunCommands_NodeSelectionStillControlsExecution(t *testing.T) {
	for _, command := range []string{"plan", "apply", "destroy"} {
		for _, selection := range []string{"all", "a", "missing"} {
			t.Run(command+"/"+selection, func(t *testing.T) {
				bp := writeRunFixture(t)
				dir := filepath.Dir(bp)
				data, err := os.ReadFile(bp)
				if err != nil {
					t.Fatal(err)
				}
				writeFixtureFile(t, bp, string(data)+`node "b" {
  source = "./module"
  runtime = runtime.fake
}`)
				writeFixtureFile(t, filepath.Join(dir, "terraform-fake"), `#!/bin/sh
printf 'runtime called: %s\n' "$1" >&2
case "$1" in
  init|plan|destroy) exit 0 ;;
  output) printf '{"id":{"value":"x"}}'; exit 0 ;;
esac
exit 1
`)
				args := []string{command, "--output", "json", "--parallelism", "2"}
				if command != "plan" {
					args = append(args, "--auto-approve")
				}
				if selection != "all" {
					args = append(args, "--node", selection)
				}
				stdout, stderr, err := runCmdAt(t, bp, args...)
				if selection == "missing" {
					if err == nil || !strings.Contains(err.Error(), `unknown node "missing"`) || stdout != "" || strings.Contains(stderr, "runtime called") {
						t.Fatalf("unknown node: stdout=%q stderr=%q error=%v", stdout, stderr, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("%s: %v", command, err)
				}
				var result runResult
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatalf("stdout = %q, want one JSON report: %v", stdout, err)
				}
				wantCount := 1
				if selection == "all" {
					wantCount = 2
				}
				if len(result.Nodes) != wantCount || result.Nodes[0].Node != "a" || (wantCount == 2 && result.Nodes[1].Node != "b") {
					t.Fatalf("nodes = %+v, want selection %s", result.Nodes, selection)
				}
				wantStatus := map[string]string{"plan": "planned", "apply": "unchanged", "destroy": "destroyed"}[command]
				for _, node := range result.Nodes {
					if node.Status != wantStatus || node.Error != "" {
						t.Fatalf("node = %+v, want status %q without errors", node, wantStatus)
					}
				}
			})
		}
	}
}
