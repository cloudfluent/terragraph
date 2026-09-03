//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// runCmdAt is runCmd pointed at an explicit blueprint path instead of the shared example fixture.
func runCmdAt(t *testing.T, blueprint string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCmd("test")
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"--blueprint", blueprint}, args...))
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// writeObserveFakeTerraform writes a stand-in terraform whose init and plan succeed and everything else — `output` included — fails, the way a real terraform with no state yet does, so an unapplied node's ports read as unknown. Minimal on purpose: the engine fake in internal/engine models far more, but observe only ever shells out to `output -json`.
func writeObserveFakeTerraform(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "terraform-fake")
	script := `#!/bin/sh
case "$1" in
  init|plan) exit 0 ;;
esac
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake terraform: %v", err)
	}
	return path
}

func writeObserveCmdFixture(t *testing.T) string {
	t.Helper()
	baseDir := t.TempDir()
	fake := writeObserveFakeTerraform(t, baseDir)
	writeFixtureFile(t, filepath.Join(baseDir, "module", "main.tf"),
		"terraform {\n  backend \"local\" {}\n}\noutput \"id\" {\n  value = \"x\"\n}\n")
	writeFixtureFile(t, filepath.Join(baseDir, "blueprint.hcl"), "node \"a\" {\n  source = \"./module\"\n  runtime = runtime.fake\n}\n\nruntime \"fake\" {\n  binary = "+strconv.Quote(fake)+"\n}\n")
	return filepath.Join(baseDir, "blueprint.hcl")
}

// TestObserve_CmdWritesLockAndJSON proves observe writes the lock beside the blueprint and prints one JSON document with the lock path, digest, and every port; the unapplied node reports unknown.
func TestObserve_CmdWritesLockAndJSON(t *testing.T) {
	bp := writeObserveCmdFixture(t)
	stdout, _, err := runCmdAt(t, bp, "observe", "--output", "json")
	if err != nil {
		t.Fatalf("observe --output json: %v", err)
	}
	var got struct {
		Lock   string `json:"lock"`
		Digest string `json:"digest"`
		Ports  []struct {
			Role       string `json:"role"`
			Port       string `json:"port"`
			Confidence string `json:"confidence"`
		} `json:"ports"`
	}
	if jerr := json.Unmarshal([]byte(stdout), &got); jerr != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%q", jerr, stdout)
	}
	if !strings.HasSuffix(got.Lock, "terragraph.lock") {
		t.Fatalf("got lock = %q, want path ending in terragraph.lock", got.Lock)
	}
	if len(got.Digest) != 64 {
		t.Fatalf("got digest = %q, want 64 hex chars", got.Digest)
	}
	if len(got.Ports) != 1 || got.Ports[0].Role != "producer" || got.Ports[0].Port != "id" || got.Ports[0].Confidence != "unknown" {
		t.Fatalf("got = %+v, want producer/id/unknown", got.Ports)
	}
	if _, serr := os.Stat(got.Lock); serr != nil {
		t.Fatalf("lock not written: %v", serr)
	}
}

// TestObserve_TextWritesLockToo proves text mode writes the same lock and keeps stdout human lines.
func TestObserve_TextWritesLockToo(t *testing.T) {
	bp := writeObserveCmdFixture(t)
	stdout, _, err := runCmdAt(t, bp, "observe")
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if !strings.Contains(stdout, "producer") || !strings.Contains(stdout, "unknown") {
		t.Fatalf("got stdout %q, want per-port lines with confidence", stdout)
	}
	if _, serr := os.Stat(filepath.Join(filepath.Dir(bp), "terragraph.lock")); serr != nil {
		t.Fatalf("lock not written: %v", serr)
	}
}
