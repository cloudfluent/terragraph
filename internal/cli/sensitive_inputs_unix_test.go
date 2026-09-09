//go:build !windows

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSensitiveInputFixture includes an invalid required field so the failure remains valid even if extra object attributes become accepted later.
func writeSensitiveInputFixture(t *testing.T, sensitive bool) string {
	t.Helper()
	bp := writeRunFixture(t)
	dir := filepath.Dir(bp)
	writeFixtureFile(t, filepath.Join(dir, "module", "main.tf"), fmt.Sprintf(`variable "credentials" {
  type = object({ count = number })
  sensitive = %t
}
`, sensitive))
	writeFixtureFile(t, bp, fmt.Sprintf(`runtime "fake" { binary = %q }
node "a" {
  source = "./module"
  runtime = runtime.fake
  vars = {
    credentials = {
      PRIVATE_PAYLOAD_KEY = "PRIVATE_PAYLOAD_VALUE"
      count = []
    }
  }
}
`, filepath.Join(dir, "terraform-fake")))
	return bp
}

func TestPlan_SensitiveInputErrorRedactsPayload(t *testing.T) {
	bp := writeSensitiveInputFixture(t, true)
	stdout, stderr, err := runCmdAt(t, bp, "plan", "--output", "json", "--log-level", "debug")
	if err == nil {
		t.Fatal("expected invalid sensitive input to fail")
	}
	for _, output := range []string{stdout, stderr, err.Error()} {
		if strings.Contains(output, "PRIVATE_PAYLOAD") {
			t.Fatal("sensitive payload appears in CLI output or error")
		}
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(cause.Error(), "PRIVATE_PAYLOAD") {
			t.Fatal("sensitive payload remains in the error chain")
		}
	}
	for _, context := range []string{"node.a.input.credentials", "module variable declaration", "sensitive", "check"} {
		if !strings.Contains(err.Error(), context) {
			t.Fatalf("error = %v, want context %q", err, context)
		}
	}
	var report runResult
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("invalid JSON report: %v", err)
	}
	if len(report.Nodes) != 1 || report.Nodes[0].Node != "a" || report.Nodes[0].Status != "failed" || report.Nodes[0].Error == "" {
		t.Fatalf("nodes = %+v, want node a failed with a safe error", report.Nodes)
	}
	if strings.Contains(stderr, "terraform init") || strings.Contains(stderr, "terraform plan") {
		t.Fatal("invalid input reached the runtime")
	}
}

// writeSensitiveEdgeFixture makes source-only sensitivity observable even when the destination variable is not marked sensitive.
func writeSensitiveEdgeFixture(t *testing.T, inputSensitive, outputSensitive bool) string {
	t.Helper()
	bp := writeSensitiveInputFixture(t, inputSensitive)
	dir := filepath.Dir(bp)
	writeFixtureFile(t, filepath.Join(dir, "producer", "main.tf"), fmt.Sprintf(`output "credentials" {
  value = {}
  sensitive = %t
}
`, outputSensitive))
	writeFixtureFile(t, filepath.Join(dir, "terraform-fake"), `#!/bin/sh
case "$1" in
  output) printf '{"credentials":{"value":{"PRIVATE_PAYLOAD_KEY":"PRIVATE_PAYLOAD_VALUE","count":[]}}}'; exit 0 ;;
  *) printf 'unexpected runtime command\n' >&2; exit 1 ;;
esac
`)
	writeFixtureFile(t, bp, fmt.Sprintf(`runtime "fake" { binary = %q }
node "producer" {
  source = "./producer"
  runtime = runtime.fake
}
node "a" {
  source = "./module"
  runtime = runtime.fake
}
edge {
  from = node.producer.output.credentials
  to = node.a.input.credentials
}
`, filepath.Join(dir, "terraform-fake")))
	return bp
}

func TestPlan_SensitiveUpstreamErrorRedactsPayload(t *testing.T) {
	bp := writeSensitiveEdgeFixture(t, false, true)
	stdout, stderr, err := runCmdAt(t, bp, "plan", "--node", "a", "--output", "json", "--log-level", "debug")
	if err == nil {
		t.Fatal("expected invalid upstream value to fail")
	}
	for _, output := range []string{stdout, stderr, err.Error()} {
		if strings.Contains(output, "PRIVATE_PAYLOAD") {
			t.Fatal("sensitive upstream payload appears in CLI output or error")
		}
	}
	if !strings.Contains(err.Error(), "node.producer.output.credentials") || !strings.Contains(err.Error(), "node.a.input.credentials") {
		t.Fatalf("error = %v, want both source and destination context", err)
	}
}

func TestRunCommands_SensitiveInputErrorsStayRedacted(t *testing.T) {
	for _, command := range []string{"plan", "apply", "destroy"} {
		for _, output := range []string{"text", "json"} {
			for _, source := range []string{"literal", "sensitive-input", "sensitive-output"} {
				t.Run(command+"/"+output+"/"+source, func(t *testing.T) {
					var bp string
					if source == "literal" {
						bp = writeSensitiveInputFixture(t, true)
					} else {
						bp = writeSensitiveEdgeFixture(t, source == "sensitive-input", source == "sensitive-output")
					}
					args := []string{command, "--node", "a", "--output", output, "--parallelism", "2", "--log-level", "debug"}
					if command != "plan" {
						args = append(args, "--auto-approve")
					}
					stdout, stderr, err := runCmdAt(t, bp, args...)
					if err == nil || !strings.Contains(err.Error(), "node.a.input.credentials") || !strings.Contains(err.Error(), "value details withheld") {
						t.Fatalf("error = %v, want safe input error with node context", err)
					}
					for _, stream := range []string{stdout, stderr} {
						if strings.Contains(stream, "PRIVATE_PAYLOAD") {
							t.Fatal("sensitive payload appears in a CLI stream")
						}
					}
					for cause := err; cause != nil; cause = errors.Unwrap(cause) {
						if strings.Contains(cause.Error(), "PRIVATE_PAYLOAD") {
							t.Fatal("sensitive payload remains in the error chain")
						}
					}
					if output == "json" {
						var report runResult
						if err := json.Unmarshal([]byte(stdout), &report); err != nil {
							t.Fatalf("invalid JSON report: %v", err)
						}
						if len(report.Nodes) != 1 || report.Nodes[0].Status != "failed" || !strings.Contains(report.Nodes[0].Error, "value details withheld") {
							t.Fatalf("nodes = %+v, want failed with redacted error", report.Nodes)
						}
					}
					if strings.Contains(stderr, "unexpected runtime command") || strings.Contains(stderr, "terraform init") || strings.Contains(stderr, "terraform plan") {
						t.Fatal("invalid input reached the consumer runtime")
					}
				})
			}
		}
	}
}

func TestPlan_NonSensitiveInputRetainsDetailedError(t *testing.T) {
	bp := writeSensitiveInputFixture(t, false)
	_, _, err := runCmdAt(t, bp, "plan", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), `attribute "count"`) || !strings.Contains(err.Error(), "number required") {
		t.Fatalf("error = %v, want detailed non-sensitive type error", err)
	}
	if errors.Unwrap(err) == nil {
		t.Fatal("non-sensitive error chain was lost")
	}
}

func TestPlan_ValidSensitiveInputStillReachesRuntime(t *testing.T) {
	bp := writeSensitiveInputFixture(t, true)
	dir := filepath.Dir(bp)
	contents, err := os.ReadFile(bp)
	if err != nil {
		t.Fatal(err)
	}
	valid := strings.Replace(string(contents), `PRIVATE_PAYLOAD_KEY = "PRIVATE_PAYLOAD_VALUE"`, "", 1)
	valid = strings.Replace(valid, "count = []", "count = 3", 1)
	writeFixtureFile(t, bp, valid)
	writeFixtureFile(t, filepath.Join(dir, "terraform-fake"), `#!/bin/sh
case "$1" in
  show) printf '%s\n' '{"format_version":"1.2","resource_changes":[]}'; exit 0 ;;
  init) mkdir -p "$TF_DATA_DIR"; exit 0 ;;
  plan)
    for arg in "$@"; do
      case "$arg" in -var-file=*) cp "${arg#-var-file=}" "$TF_DATA_DIR/received.json"; exit $? ;; esac
    done
    ;;
esac
exit 1
`)
	stdout, _, err := runCmdAt(t, bp, "plan", "--output", "json")
	if err != nil {
		t.Fatalf("valid sensitive input failed: %v", err)
	}
	var report runResult
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Nodes) != 1 || report.Nodes[0].Status != "planned" {
		t.Fatalf("nodes = %+v, want planned", report.Nodes)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".terragraph", "tfdata", "a", "received.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Credentials struct{ Count int } }
	if err := json.Unmarshal(data, &got); err != nil || got.Credentials.Count != 3 {
		t.Fatalf("received count = %d, error = %v, want 3", got.Credentials.Count, err)
	}
}
