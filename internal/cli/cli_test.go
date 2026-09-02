package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// runCmd executes the terragraph command tree with args and returns stdout, stderr, and any error. It targets examples/group/blueprint.hcl (relative local module sources, no terraform/tofu binary or network access required, since engine.Load only parses .tf files and never shells out).
func runCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCmd("test")
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"--blueprint", "../../examples/group/blueprint.hcl"}, args...))
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestGraph_DefaultText_MatchesExampleOutput(t *testing.T) {
	// Byte-for-byte guard: examples/group/README.md asserts this exact output.
	stdout, _, err := runCmd(t, "graph")
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	want := "level 1: vpc\nlevel 2: checkout.cluster\nlevel 3: checkout.nodegroup\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestGraph_OutputJSON(t *testing.T) {
	stdout, _, err := runCmd(t, "graph", "--output", "json")
	if err != nil {
		t.Fatalf("graph --output json: %v", err)
	}
	var got graphResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", stdout, err)
	}
	want := [][]string{{"vpc"}, {"checkout.cluster"}, {"checkout.nodegroup"}}
	if len(got.Levels) != len(want) {
		t.Fatalf("levels = %v, want %v", got.Levels, want)
	}
	for i := range want {
		if len(got.Levels[i]) != 1 || got.Levels[i][0] != want[i][0] {
			t.Fatalf("levels[%d] = %v, want %v", i, got.Levels[i], want[i])
		}
	}
}

func TestGraph_OutputJSON_WithFormatDot_Errors(t *testing.T) {
	_, _, err := runCmd(t, "graph", "--format", "dot", "--output", "json")
	if err == nil {
		t.Fatalf("expected an error combining --format dot with --output json")
	}
	if !strings.Contains(err.Error(), "--output json is not supported with --format dot") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_OutputJSON_Valid(t *testing.T) {
	stdout, _, err := runCmd(t, "validate", "--output", "json")
	if err != nil {
		t.Fatalf("validate --output json: %v", err)
	}
	var got validateResult
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", stdout, err)
	}
	if !got.Valid {
		t.Fatalf("expected valid=true, got %+v", got)
	}
	if len(got.Problems) != 0 {
		t.Fatalf("expected no problems, got %+v", got.Problems)
	}
}

func TestVendor_OutputJSON_NothingToVendor(t *testing.T) {
	// examples/group/blueprint.hcl uses only local module sources, so vendor.All has nothing to do: this exercises the empty-array JSON shape without needing network access.
	stdout, _, err := runCmd(t, "vendor", "--output", "json")
	if err != nil {
		t.Fatalf("vendor --output json: %v", err)
	}
	var got []vendorResultDTO
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", stdout, err)
	}
	if len(got) != 0 {
		t.Fatalf("expected an empty result array, got %+v", got)
	}
}

func TestRootCmd_LogLevel_RejectsUnknownValue(t *testing.T) {
	_, _, err := runCmd(t, "--log-level", "bogus", "graph")
	if err == nil {
		t.Fatalf("expected an error for an unknown --log-level value")
	}
	if !strings.Contains(err.Error(), `unknown log level "bogus"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRootCmd_LogLevel_DebugSurfacesInternalTracing(t *testing.T) {
	_, stderr, err := runCmd(t, "--log-level", "debug", "graph")
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if !strings.Contains(stderr, "blueprint loaded") {
		t.Fatalf("expected a debug trace line on stderr, got %q", stderr)
	}
}

func TestRootCmd_LogLevel_DefaultStaysSilent(t *testing.T) {
	_, stderr, err := runCmd(t, "graph")
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr output at the default log level, got %q", stderr)
	}
}
