package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestGraph_SelectionJSONQualifiedAndStable(t *testing.T) {
	args := []string{"graph", "--node", "checkout.cluster", "--node", "checkout.cluster", "--downstream", "--output", "json"}
	out, _, err := runCmd(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	var result graphResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Levels, [][]string{{"checkout.cluster"}, {"checkout.nodegroup"}}) || result.Selection == nil || len(result.Selection.Nodes) != 2 || len(result.Selection.BoundaryEdges) != 1 {
		t.Fatalf("got = %s", out)
	}
	if strings.Contains(out, "null") || strings.Contains(out, "backend") || strings.Contains(out, "payments") {
		t.Fatalf("got = %s, want only selection relationships", out)
	}
	out2, _, err := runCmd(t, "graph", "--node", "checkout.cluster", "--downstream", "--output", "json")
	if err != nil || out != out2 {
		t.Fatalf("got = %q, %v, want %q", out2, err, out)
	}
}

func TestGraph_SelectionTextAndDOT(t *testing.T) {
	out, _, err := runCmd(t, "graph", "--node", "checkout.cluster", "--downstream")
	for _, want := range []string{"selection: downstream", "requested: checkout.cluster", "checkout.nodegroup: downstream of checkout.cluster", "vpc.output.vpc_id -> checkout.cluster.input.vpc_id (data)", "level 1: checkout.cluster\nlevel 2: checkout.nodegroup\n"} {
		if err != nil || !strings.Contains(out, want) {
			t.Fatalf("got = %q, %v, want %q", out, err, want)
		}
	}
	out, _, err = runCmd(t, "graph", "--node", "checkout.cluster", "--format", "dot")
	if err != nil || !strings.Contains(out, "checkout.nodegroup (not selected)") || !strings.Contains(out, "vpc (not selected)") || strings.Contains(out, "payments") {
		t.Fatalf("got = %q, %v", out, err)
	}
}

func TestGraph_DownstreamFalsePreservesDefaultOutput(t *testing.T) {
	a, _, err := runCmd(t, "graph", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := runCmd(t, "graph", "--output", "json", "--downstream=false")
	if err != nil || a != b || strings.Contains(b, "selection") {
		t.Fatalf("got = %q, %v, want %q", b, err, a)
	}
}

func TestSelection_RejectsInvalidCLIInputs(t *testing.T) {
	for _, command := range []string{"graph", "plan", "apply", "destroy"} {
		for _, flags := range [][]string{{"positional"}, {"--node", ""}, {"--downstream"}, {"--node", "vpc,checkout.cluster"}, {"--node", "vpc", "--node", "missing"}, {"--node", "checkout"}} {
			args := append([]string{command}, flags...)
			if command == "apply" || command == "destroy" {
				args = append(args, "--auto-approve")
			}
			_, _, err := runCmd(t, args...)
			if err == nil {
				t.Fatalf("args = %q, want error", args)
			}
		}
	}
}

func TestSelection_SavedOverridesRejectedBeforeLoad(t *testing.T) {
	for _, args := range [][]string{{"apply", "--plan", "run-missing"}, {"plan", "--save", "--continue", "run-missing"}} {
		for _, flags := range [][]string{{"--node", ""}, {"--node", "vpc"}, {"--downstream"}, {"--downstream=false"}, {"--upstream"}, {"--upstream=false"}} {
			call := append([]string{"--blueprint", "missing-directory"}, args...)
			call = append(call, flags...)
			_, _, err := runRootCmd(t, call...)
			if err == nil || !strings.Contains(err.Error(), "omit --node, --downstream, and --upstream") {
				t.Fatalf("args = %q, got = %v", call, err)
			}
		}
	}
}

func TestGraph_UpstreamUsesExistingScopeJSON(t *testing.T) {
	out, _, err := runCmd(t, "graph", "--node", "checkout.cluster", "--upstream", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result graphResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Levels, [][]string{{"vpc"}, {"checkout.cluster"}}) || result.Selection.Mode != "upstream" {
		t.Fatalf("got = %s, want vpc then selected cluster", out)
	}
}
