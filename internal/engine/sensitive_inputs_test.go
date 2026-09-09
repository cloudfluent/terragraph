package engine

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func TestPlan_SensitiveInputErrorDoesNotRetainPayload(t *testing.T) {
	dir := t.TempDir()
	if err := osWriteFile(filepath.Join(dir, "module", "main.tf"), []byte(`variable "credentials" {
  type = object({ count = number, token = optional(string, "PRIVATE_PAYLOAD_DEFAULT") })
  sensitive = true
}`)); err != nil {
		t.Fatal(err)
	}
	bp := writeBlueprint(t, dir, `node "a" {
  source = "./module"
  vars = { credentials = { PRIVATE_PAYLOAD_KEY = "PRIVATE_PAYLOAD_VALUE", count = [] } }
}`)
	var stdout, stderr bytes.Buffer
	e, err := Load(bp, exec.Binary(filepath.Join(dir, "must-not-run")), &stdout, &stderr)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	runs, err := e.Plan(Options{})
	if err == nil || !strings.Contains(err.Error(), "node.a.input.credentials") || !strings.Contains(err.Error(), "value details withheld") {
		t.Fatalf("error = %v, want redacted input validation error", err)
	}
	if len(runs.Nodes) != 1 || runs.Nodes[0].Status != StatusFailed || runs.Nodes[0].Err == nil {
		t.Fatalf("runs = %+v, want a failed run with a safe error", runs)
	}
	for _, reported := range []error{err, runs.Nodes[0].Err} {
		for cause := reported; cause != nil; cause = errors.Unwrap(cause) {
			if strings.Contains(cause.Error(), "PRIVATE_PAYLOAD") {
				t.Fatal("sensitive payload remains in an error chain")
			}
		}
	}
	if strings.Contains(stdout.String()+stderr.String(), "PRIVATE_PAYLOAD") {
		t.Fatal("sensitive payload appears in engine streams")
	}
}
