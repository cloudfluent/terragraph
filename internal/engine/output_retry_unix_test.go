//go:build !windows

package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApply_OutputRetryDoesNotRepeatMutation(t *testing.T) {
	e, _, log := loadApplyTestEngine(t)
	e.OutputRetries = 1
	t.Setenv("TG_RETRY_MARKER", filepath.Join(e.BaseDir, "output-attempted"))
	path := string(e.Binary)
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script = bytes.Replace(script, []byte("  output)"), []byte("  output)\n if [ ! -f \"$TG_RETRY_MARKER\" ]; then touch \"$TG_RETRY_MARKER\"; exit 7; fi\n"), 1)
	if err := os.WriteFile(path, script, 0700); err != nil {
		t.Fatal(err)
	}
	runs, err := e.Apply(Options{AutoApprove: true})
	if err != nil || runs.Nodes[0].Status != StatusApplied {
		t.Fatalf("got = %+v, %v", runs, err)
	}
	calls, err := os.ReadFile(log)
	if err != nil || strings.Count(string(calls), "apply\n") != 1 {
		t.Fatalf("calls = %s, error = %v, want one mutation", calls, err)
	}
	if _, err := os.Stat(filepath.Join(e.BaseDir, "output-attempted")); err != nil {
		t.Fatal(err)
	}
}
