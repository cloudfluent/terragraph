//go:build !windows

package engine

import (
	"errors"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func reviewFixture(t *testing.T, body string) *Engine {
	t.Helper()
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "module")
	if err := os.Mkdir(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	config := "terraform {\n backend \"local\" {}\n}\noutput \"id\" { value = \"existing\" }\nvariable \"input\" {\n type = string\n default = \"none\"\n}\n"
	if err := os.WriteFile(filepath.Join(moduleDir, "main.tf"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	runtime := filepath.Join(dir, "runtime")
	script := `#!/bin/sh
name=$(basename "$TF_DATA_DIR")
printf '%s %s\n' "$name" "$1" >> "$TG_REVIEW_CALLS"
case "$1" in
init) exit 0 ;;
plan)
 if [ "$name" = failed ]; then echo 'provider credential rejected' >&2; exit 1; fi
 for arg in "$@"; do
  case "$arg" in
  -out=*)
   path="${arg#-out=}"
   if [ ! -f "$path" ]; then exit 41; fi
   printf 'sensitive artifact' > "$path"
   ;;
  esac
 done
 case "$name" in unchanged|outputonly) exit 0 ;; *) exit 2 ;; esac
 ;;
show)
 case "$name" in
 created) action='["create"]' ;;
 updated) action='["update"]' ;;
 deleted) action='["delete"]' ;;
 replaced) action='["delete","create"]' ;;
 unchanged) action='["no-op"]' ;;
 outputonly) printf '%s\n' '{"format_version":"1.2","output_changes":{"id":{"actions":["update"],"before":"CANARY_BEFORE","after":"CANARY_AFTER"}}}'; exit 0 ;;
 *) action='["create"]' ;;
 esac
 printf '{"format_version":"1.2","resource_changes":[{"address":"terraform_data.%s","change":{"actions":%s,"after":{"secret":"CANARY_RESOURCE"}}}],"output_changes":{}}\n' "$name" "$action"
 ;;
output)
 if [ "$TG_REVIEW_OUTPUT_FAIL" = 1 ]; then echo 'credential rejected' >&2; exit 1; fi
 printf '%s\n' '{"id":{"value":"existing","sensitive":false}}'
 ;;
apply) echo 'unexpected apply' >&2; exit 99 ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(runtime, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "blueprint.hcl")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_REVIEW_CALLS", filepath.Join(dir, "calls"))
	e, err := Load(path, exec.Binary(runtime), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func reviewNode(name string) string { return "node \"" + name + "\" { source = \"./module\" }\n" }

func TestReviewPlan_ActionsOutputOnlyPolicyAndCleanup(t *testing.T) {
	names := []string{"created", "updated", "deleted", "replaced", "unchanged", "outputonly"}
	body := ""
	for _, name := range names {
		body += reviewNode(name)
	}
	e := reviewFixture(t, body)
	runs, err := e.ReviewPlan(Options{Parallelism: 3}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != len(names) {
		t.Fatalf("got = %d", len(runs))
	}
	for _, run := range runs {
		review := run.Review
		if review == nil || !review.Evidence || review.HasChanges == nil {
			t.Fatalf("missing evidence: %+v", run)
		}
		if *review.HasChanges != (run.Node != "unchanged") {
			t.Fatalf("wrong changes for %s", run.Node)
		}
		want := "pass"
		if run.Node == "deleted" || run.Node == "replaced" {
			want = "block"
		}
		if review.PolicyDecision != want {
			t.Fatalf("%s policy = %s, want %s", run.Node, review.PolicyDecision, want)
		}
		if run.Node == "outputonly" && (len(review.Outputs) != 1 || len(review.Resources) != 0) {
			t.Fatalf("output-only plan lost: %+v", review)
		}
		if _, err := os.Stat(e.planPath(run.Node)); !os.IsNotExist(err) {
			t.Fatalf("plan survived: %v", err)
		}
	}
	calls, _ := os.ReadFile(os.Getenv("TG_REVIEW_CALLS"))
	if strings.Contains(string(calls), "apply") {
		t.Fatal("inspection applied")
	}
}

func TestReviewPlan_PreservesIndependentResults(t *testing.T) {
	body := reviewNode("failed") + reviewNode("created") + reviewNode("blocked") + reviewNode("independent")
	body += "edge {\n from = node.failed\n to = node.blocked\n}\nedge {\n from = node.created\n to = node.independent\n}\n"
	e := reviewFixture(t, body)
	runs, err := e.ReviewPlan(Options{Parallelism: 2}, false)
	if err == nil {
		t.Fatal("failed plan must fail run")
	}
	byName := map[string]NodeRun{}
	for _, run := range runs {
		byName[run.Node] = run
	}
	if byName["independent"].Status != StatusPlanned || !byName["independent"].Review.Evidence {
		t.Fatal("independent descendant lost")
	}
	if byName["blocked"].Status != StatusNotRun || byName["blocked"].Review.Diagnostic.Code != "dependency_not_reached" || byName["blocked"].Review.HasChanges != nil {
		t.Fatalf("got = %+v", byName["blocked"])
	}
	if byName["failed"].Review.Diagnostic.Code != "plan_failed" {
		t.Fatal("provider error lost")
	}
}

func TestReviewPlan_UpstreamMissingDiffersFromCredentialFailure(t *testing.T) {
	body := reviewNode("created") + reviewNode("consumer") + "edge {\n from = node.created.output.id\n to = node.consumer.input.input\n}\n"
	e := reviewFixture(t, body)
	t.Setenv("TG_REVIEW_OUTPUT_FAIL", "1")
	runs, err := e.ReviewPlan(Options{Node: "consumer"}, false)
	if err == nil || runs[0].Review.Diagnostic.Code != "upstream_output_unavailable" || runs[0].Review.HasChanges != nil {
		t.Fatalf("got = %+v, %v", runs, err)
	}
	state := filepath.Join(e.BaseDir, ".terragraph", "state", "created.tfstate")
	if err := os.MkdirAll(filepath.Dir(state), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_REVIEW_OUTPUT_FAIL", "1")
	runs, err = e.ReviewPlan(Options{Node: "consumer"}, false)
	if err == nil || runs[0].Review.Diagnostic.Code != "input_resolution_failed" || strings.Contains(err.Error(), "has not been applied") {
		t.Fatalf("got = %+v, %v", runs, err)
	}
}

func TestReviewPlan_LiveAndSnapshotBasis(t *testing.T) {
	body := "snapshots {}\n" + reviewNode("created") + reviewNode("consumer") + "edge {\n from = node.created.output.id\n to = node.consumer.input.input\n}\n"
	e := reviewFixture(t, body)
	runs, err := e.ReviewPlan(Options{Node: "consumer"}, false)
	if err != nil || runs[0].Review.Inputs[0].Source != "live" || len(runs[0].Review.Limitations) < 2 {
		t.Fatalf("got = %+v, %v", runs, err)
	}
	sensitive := false
	if err := e.writeSnapshot("created", exec.Outputs{"id": {Value: "snapshot", Sensitive: &sensitive}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_REVIEW_OUTPUT_FAIL", "1")
	runs, err = e.ReviewPlan(Options{Node: "consumer"}, false)
	if err != nil || runs[0].Review.Inputs[0].Source != "snapshot" {
		t.Fatalf("got = %+v, %v", runs, err)
	}
}

func TestReviewPlan_DeclaredPolicyWins(t *testing.T) {
	e := reviewFixture(t, "node \"created\" {\n source = \"./module\"\n approve = \"none\"\n}\n")
	runs, err := e.ReviewPlan(Options{Approve: "all"}, false)
	if err != nil || runs[0].Review.Policy != "none" || runs[0].Review.PolicyDecision != "block" {
		t.Fatalf("got = %+v, %v", runs, err)
	}
}

func TestReviewPlan_UnsupportedBackendKeepsTextPreview(t *testing.T) {
	e := reviewFixture(t, reviewNode("unchanged"))
	if err := os.WriteFile(filepath.Join(e.BaseDir, "module", "main.tf"), []byte("terraform {\n backend \"remote\" {}\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(filepath.Join(e.BaseDir, "blueprint.hcl"), e.Binary, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := loaded.ReviewPlan(Options{}, false)
	if err == nil || runs[0].Review.Diagnostic.Code != "inspection_unsupported" || runs[0].Review.HasChanges != nil {
		t.Fatalf("got = %+v, %v", runs, err)
	}
	runs, err = loaded.ReviewPlan(Options{}, true)
	if err != nil || runs[0].Status != StatusPlanned || runs[0].Review.Evidence {
		t.Fatalf("got = %+v, %v", runs, err)
	}
	calls, _ := os.ReadFile(os.Getenv("TG_REVIEW_CALLS"))
	if strings.Contains(string(calls), "show") || strings.Contains(string(calls), "apply") {
		t.Fatalf("unsupported inspection invoked: %s", calls)
	}
}

func TestReviewPlan_InspectionFailureRemovesPlan(t *testing.T) {
	e := reviewFixture(t, reviewNode("created"))
	data, err := os.ReadFile(string(e.Binary))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(string(e.Binary), []byte(strings.Replace(string(data), "show)\n", "show) exit 1 ;;\nunused)\n", 1)), 0700); err != nil {
		t.Fatal(err)
	}
	runs, err := e.ReviewPlan(Options{}, false)
	if err == nil || runs[0].Review.Diagnostic.Code != "inspection_failed" || runs[0].Review.HasChanges != nil {
		t.Fatalf("got = %+v, %v", runs, err)
	}
	if _, err := os.Stat(e.planPath("created")); !os.IsNotExist(err) {
		t.Fatal("failed inspection retained sensitive plan")
	}
}

func TestApply_MissingUpstreamStatePreservesReadFailure(t *testing.T) {
	e := reviewFixture(t, reviewNode("created")+reviewNode("consumer")+"edge {\n from = node.created.output.id\n to = node.consumer.input.input\n}\n")
	t.Setenv("TG_REVIEW_OUTPUT_FAIL", "1")
	_, err := e.Apply(Options{Node: "consumer", AutoApprove: true})
	assertMissingUpstreamState(t, e, err)
}

func TestDestroy_MissingUpstreamStatePreservesReadFailure(t *testing.T) {
	e := reviewFixture(t, reviewNode("created")+reviewNode("consumer")+"edge {\n from = node.created.output.id\n to = node.consumer.input.input\n}\n")
	t.Setenv("TG_REVIEW_OUTPUT_FAIL", "1")
	_, err := e.Destroy(Options{Node: "consumer", AutoApprove: true})
	assertMissingUpstreamState(t, e, err)
}

func assertMissingUpstreamState(t *testing.T, e *Engine, err error) {
	t.Helper()
	var runtimeError *osexec.ExitError
	if !errors.Is(err, errUpstreamOutputMissing) || !errors.As(err, &runtimeError) || !strings.Contains(err.Error(), "recover existing local state") {
		t.Fatalf("got = %v, want missing-state classification, original runtime cause, and remedy", err)
	}
	calls, readErr := os.ReadFile(filepath.Join(e.BaseDir, "calls"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(calls), "consumer apply") || strings.Contains(string(calls), "consumer destroy") {
		t.Fatalf("unavailable inputs reached a mutation: %s", calls)
	}
}
