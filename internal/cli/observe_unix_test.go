//go:build !windows

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeObservationFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fake := filepath.Join(dir, "runtime")
	writeFixtureFile(t, fake, `#!/bin/sh
echo CANARY_DIAGNOSTIC >&2
case "$1" in
init) exit 0 ;;
output) printf '%s\n' '{"large":{"value":9007199254740993,"sensitive":false},"secret":{"value":"CANARY_SECRET","sensitive":false},"unknown":{"value":"CANARY_UNKNOWN"},"null":{"value":null,"sensitive":false},"false":{"value":false,"sensitive":false}}' ;;
*) exit 1 ;;
esac
`)
	if err := os.Chmod(fake, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(dir, "module", "main.tf"), "terraform {\n backend \"local\" {}\n}\noutput \"secret\" {\n value = \"CANARY_SECRET\"\n sensitive = true\n}\n")
	writeFixtureFile(t, filepath.Join(dir, "module", ".terraform.lock.hcl"), "")
	writeFixtureFile(t, filepath.Join(dir, ".terragraph", "state", "a.tfstate"), "{}")
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), fmt.Sprintf("runtime \"fake\" {\n binary = %q\n}\nnode \"a\" {\n source = \"./module\"\n runtime = runtime.fake\n}\nnode \"b\" {\n source = \"./module\"\n runtime = runtime.fake\n}\n", fake))
	return filepath.Join(dir, "blueprint.hcl")
}

func TestOutput_RedactionAndPartialFailure(t *testing.T) {
	bp := writeObservationFixture(t)
	stdout, stderr, err := runCmdAt(t, bp, "output", "--output", "json")
	if err == nil {
		t.Fatal("missing sibling state must fail")
	}
	if strings.Contains(stdout+stderr+err.Error(), "CANARY") {
		t.Fatalf("sensitive canary leaked: %s %s %v", stdout, stderr, err)
	}
	if !strings.Contains(stdout, "9007199254740993") {
		t.Fatal("integer precision lost")
	}
	var result observationResultDTO
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 2 || result.Nodes[0].Status != "observed" || result.Nodes[1].Diagnostics[0].Code != "local_state_unavailable" {
		t.Fatalf("got = %+v", result)
	}
	if !result.Nodes[0].Outputs["secret"].Redacted || !result.Nodes[0].Outputs["unknown"].Redacted {
		t.Fatal("sensitivity weakened")
	}
}

func TestOutput_RawDisclosureAndPrecision(t *testing.T) {
	bp := writeObservationFixture(t)
	stdout, _, err := runCmdAt(t, bp, "output", "--node", "a", "large", "--raw")
	if err != nil || stdout != "9007199254740993" {
		t.Fatalf("got = %q, %v", stdout, err)
	}
	stdout, stderr, err := runCmdAt(t, bp, "output", "--node", "a", "unknown", "--raw")
	if err == nil || stdout != "" || !strings.Contains(stderr, "--show-sensitive") {
		t.Fatalf("got = %q %q %v", stdout, stderr, err)
	}
	stdout, _, err = runCmdAt(t, bp, "output", "--node", "a", "unknown", "--raw", "--show-sensitive")
	if err != nil || stdout != "CANARY_UNKNOWN" {
		t.Fatalf("got = %q, %v", stdout, err)
	}
}

func TestOutput_MissingNameAndArguments(t *testing.T) {
	bp := writeObservationFixture(t)
	stdout, _, err := runCmdAt(t, bp, "output", "--node", "a", "missing", "--output", "json")
	if err == nil || !strings.Contains(stdout, "output_not_found") {
		t.Fatalf("got = %q, %v", stdout, err)
	}
	stdout, _, err = runCmdAt(t, bp, "output", "large", "--output", "json")
	if err == nil || !strings.Contains(stdout, "invalid_arguments") {
		t.Fatalf("got = %q, %v", stdout, err)
	}
	stdout, _, err = runCmdAt(t, filepath.Join(t.TempDir(), "missing"), "output", "--output", "json")
	if err == nil || !strings.Contains(stdout, "observation_load_failed") {
		t.Fatalf("got = %q, %v", stdout, err)
	}
}
