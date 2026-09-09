//go:build !windows

package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
)

func selectionEngine(t *testing.T) *Engine {
	t.Helper()
	body := ""
	for _, name := range []string{"a", "b", "c", "d", "x", "p", "q"} {
		body += reviewNode(name)
	}
	body += `edge {
 from = node.a.output.id
 to = node.b.input.input
}
edge {
 from = node.b.output.id
 to = node.c.input.input
}
edge {
 from = node.x.output.id
 to = node.c.input.extra
}
edge {
 from = node.c
 to = node.d
}
edge {
 from = node.p
 to = node.q
}
`
	e := reviewFixture(t, body)
	modulePath := filepath.Join(e.BaseDir, "module", "main.tf")
	src, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatal(err)
	}
	src = append(src, []byte("variable \"extra\" {\n type = string\n default = \"none\"\n}\n")...)
	if err := os.WriteFile(modulePath, src, 0600); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(string(e.Binary))
	if err != nil {
		t.Fatal(err)
	}
	script = bytes.Replace(script, []byte("case \"$1\" in"), []byte(`if [ "$1" = plan ] || [ "$1" = destroy ]; then
 for arg in "$@"; do
  case "$arg" in -var-file=*) cp "${arg#-var-file=}" "$TG_SELECTION_DIR/$name.inputs.json" ;; esac
 done
fi
case "$1" in
version) printf '%s' '{"terraform_version":"1.5.7","platform":"fixture"}'; exit 0 ;;
apply|destroy) exit 0 ;;`), 1)
	script = bytes.Replace(script, []byte("case \"$name\" in unchanged|outputonly)"), []byte("case \"$name\" in b|unchanged|outputonly)"), 1)
	script = bytes.Replace(script, []byte("unchanged) action="), []byte("b|unchanged) action="), 1)
	if err := os.WriteFile(string(e.Binary), script, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_SELECTION_DIR", e.BaseDir)
	loaded, err := Load(filepath.Join(e.BaseDir, "blueprint.hcl"), e.Binary, e.Stdout, e.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func selectionCalls(t *testing.T, e *Engine) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.BaseDir, "calls"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertSelectedRuns(t *testing.T, runs []NodeRun, want []string) {
	t.Helper()
	names := []string{}
	for _, r := range runs {
		names = append(names, r.Node)
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got = %v, want %v", names, want)
	}
}

func TestApply_SelectionReadsExternalInputsAndPlansAfterNoOp(t *testing.T) {
	e := selectionEngine(t)
	announced := false
	opts := Options{Nodes: []string{"b"}, Downstream: true, AutoApprove: true, OnSelection: func(s *graph.Selection, levels [][]string) {
		if selectionCalls(t, e) != "" {
			t.Fatal("scope was announced after runtime started")
		}
		if !reflect.DeepEqual(s.Names(), []string{"b", "c", "d"}) || len(levels) != 3 {
			t.Fatalf("got = %+v, %v", s, levels)
		}
		announced = true
	}}
	runs, err := e.Apply(opts)
	if err != nil {
		t.Fatal(err)
	}
	assertSelectedRuns(t, runs, []string{"b", "c", "d"})
	if !announced || runs[0].Status != StatusUnchanged || runs[1].Status != StatusApplied {
		t.Fatalf("got = %+v, announced = %t", runs, announced)
	}
	calls := selectionCalls(t, e)
	for _, name := range []string{"a", "x", "p", "q"} {
		for _, command := range []string{"plan", "apply", "destroy", "init"} {
			if strings.Contains(calls, name+" "+command+"\n") {
				t.Fatalf("got = %s, excluded node executed", calls)
			}
		}
	}
	if strings.Count(calls, "c plan\n") != 1 || strings.Count(calls, "d plan\n") != 1 || !strings.Contains(calls, "a output\n") || !strings.Contains(calls, "x output\n") {
		t.Fatalf("got = %s", calls)
	}
	inputs, err := os.ReadFile(filepath.Join(e.BaseDir, "c.inputs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(inputs, &values); err != nil {
		t.Fatal(err)
	}
	if values["input"] != "existing" || values["extra"] != "existing" {
		t.Fatalf("got = %s", inputs)
	}
	records, err := e.ListExecutions()
	if err != nil || len(records) != 1 || records[0].Selection == nil || len(records[0].Nodes) != 3 {
		t.Fatalf("got = %+v, %v", records, err)
	}
}

func TestApply_MultipleSeedsRunConvergenceOnce(t *testing.T) {
	e := selectionEngine(t)
	runs, err := e.Apply(Options{Nodes: []string{"x", "b", "b"}, Downstream: true, AutoApprove: true, Parallelism: 2})
	if err != nil {
		t.Fatal(err)
	}
	// Full-graph levels retain x before b, even though a is not selected.
	assertSelectedRuns(t, runs, []string{"x", "b", "c", "d"})
	if calls := selectionCalls(t, e); strings.Count(calls, "c plan\n") != 1 || strings.Count(calls, "c apply\n") != 1 {
		t.Fatalf("got = %s", calls)
	}
}

func TestApply_SelectionKeepsDuplicateDataEdges(t *testing.T) {
	e := selectionEngine(t)
	path := filepath.Join(e.BaseDir, "blueprint.hcl")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.ReplaceAll(body, []byte("node.x.output.id"), []byte("node.b.output.id"))
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	e, err = Load(path, e.Binary, e.Stdout, e.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := e.Apply(Options{Nodes: []string{"b"}, Downstream: true, AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	assertSelectedRuns(t, runs, []string{"b", "c", "d"})
	calls := selectionCalls(t, e)
	if strings.Count(calls, "c plan\n") != 1 || strings.Contains(calls, "x output\n") {
		t.Fatalf("got = %s", calls)
	}
	data, err := os.ReadFile(filepath.Join(e.BaseDir, "c.inputs.json"))
	if err != nil || !strings.Contains(string(data), `"extra": "existing"`) || !strings.Contains(string(data), `"input": "existing"`) {
		t.Fatalf("got = %s, %v", data, err)
	}
}

func TestDestroy_SelectionReversesOrderAndPreservesPolicy(t *testing.T) {
	e := selectionEngine(t)
	runs, err := e.Destroy(Options{Nodes: []string{"b"}, Downstream: true, AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	assertSelectedRuns(t, runs, []string{"d", "c", "b"})
	calls := selectionCalls(t, e)
	for _, name := range []string{"a", "x", "p", "q"} {
		if strings.Contains(calls, name+" destroy\n") {
			t.Fatalf("got = %s", calls)
		}
	}
	if _, err := e.Destroy(Options{Nodes: []string{"b"}, Downstream: true, Parallelism: 2}); err == nil || !strings.Contains(err.Error(), "--auto-approve") {
		t.Fatalf("got = %v", err)
	}
}

func TestApply_SelectionOutputFailureNeverExpandsScope(t *testing.T) {
	e := selectionEngine(t)
	t.Setenv("TG_REVIEW_OUTPUT_FAIL", "1")
	runs, err := e.Apply(Options{Nodes: []string{"b"}, Downstream: true, AutoApprove: true})
	if err == nil {
		t.Fatal("missing outputs accepted")
	}
	assertSelectedRuns(t, runs, []string{"b", "c", "d"})
	if runs[0].Status != StatusFailed || runs[1].Status != StatusNotRun {
		t.Fatalf("got = %+v", runs)
	}
	calls := selectionCalls(t, e)
	if strings.Contains(calls, " plan\n") || strings.Contains(calls, " apply\n") {
		t.Fatalf("got = %s", calls)
	}
}

func TestSavedExecution_SelectionStaysFixedAcrossFrontiers(t *testing.T) {
	e := selectionEngine(t)
	record, err := e.SavePlans(Options{Nodes: []string{"b"}, Downstream: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Nodes) != 3 || record.Selection == nil || record.Nodes[0].Phase != "planned" || record.Nodes[1].Phase != "pending" {
		t.Fatalf("got = %+v", record)
	}
	for _, want := range []string{"b", "c", "d"} {
		runs, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true})
		if err != nil {
			t.Fatal(err)
		}
		assertSelectedRuns(t, runs, []string{want})
		if want != "d" {
			next, err := e.SavePlans(Options{}, record.ID)
			if err != nil || !reflect.DeepEqual(next.Selection, record.Selection) || len(next.Nodes) != 3 {
				t.Fatalf("got = %+v, %v", next, err)
			}
		}
	}
	calls := selectionCalls(t, e)
	for _, name := range []string{"a", "x", "p", "q"} {
		if strings.Contains(calls, name+" plan\n") || strings.Contains(calls, name+" apply\n") {
			t.Fatalf("got = %s", calls)
		}
	}
	for _, name := range []string{"b", "c", "d"} {
		if strings.Count(calls, name+" plan\n") != 1 {
			t.Fatalf("got = %s", calls)
		}
	}
}

func TestSavedExecution_SelectionOverridesNeverCallRuntime(t *testing.T) {
	e := selectionEngine(t)
	for _, opts := range []Options{{Nodes: []string{"b"}}, {Nodes: []string{""}}, {Downstream: true}, {SelectionSpecified: true}} {
		if _, err := e.SavePlans(opts, "run-missing"); err == nil || !strings.Contains(err.Error(), "omit --node and --downstream") {
			t.Fatalf("got = %v", err)
		}
		if _, err := e.ApplySavedPlans("run-missing", opts); err == nil || !strings.Contains(err.Error(), "omit --node and --downstream") {
			t.Fatalf("got = %v", err)
		}
	}
	if calls := selectionCalls(t, e); calls != "" {
		t.Fatalf("got = %s", calls)
	}
}

func TestSavedExecution_ContinuationRuntimeUsesRecordedMembership(t *testing.T) {
	e := selectionEngine(t)
	record, err := e.SavePlans(Options{Nodes: []string{"b"}, Downstream: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	// An unrelated module's file requirement would reject this Terraform binary if continuation mistook nil options for all nodes.
	schema := *e.Graph.Nodes["p"].Schema
	schema.RequiresTofuFiles = true
	e.Graph.Nodes["p"].Schema = &schema
	if _, err := e.SavePlans(Options{}, record.ID); err != nil {
		t.Fatal(err)
	}
	upstreamSchema := *e.Graph.Nodes["x"].Schema
	upstreamSchema.RequiresTofuFiles = true
	e.Graph.Nodes["x"].Schema = &upstreamSchema
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err == nil || !strings.Contains(err.Error(), "node.x.runtime") {
		t.Fatalf("got = %v, want direct upstream compatibility check", err)
	}
}

func TestSavedExecution_OldRecordKeepsMembershipWithoutMetadata(t *testing.T) {
	e := selectionEngine(t)
	record, err := e.SavePlans(Options{Nodes: []string{"b"}, Downstream: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := e.openExecutionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.close() }()
	stored, rev, err := readExecutionRecord(e.context(), store, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Selection = nil
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.write(e.context(), record.ID+".json", data, rev); err != nil {
		t.Fatal(err)
	}
	var selection *graph.Selection
	runs, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true, OnSelection: func(s *graph.Selection, _ [][]string) { selection = s }})
	if err != nil || selection != nil {
		t.Fatalf("got = %+v, %v", selection, err)
	}
	assertSelectedRuns(t, runs, []string{"b"})
	next, err := e.SavePlans(Options{}, record.ID)
	if err != nil || next.Selection != nil || len(next.Nodes) != 3 {
		t.Fatalf("got = %+v, %v", next, err)
	}
}

func TestSavedExecution_CorruptSelectionCannotExecuteOrBePublished(t *testing.T) {
	e := selectionEngine(t)
	record, err := e.SavePlans(Options{Nodes: []string{"b"}, Downstream: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := e.openExecutionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.close() }()
	stored, rev, err := readExecutionRecord(e.context(), store, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Selection.Nodes = stored.Selection.Nodes[:1]
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.write(e.context(), record.ID+".json", data, rev); err != nil {
		t.Fatal(err)
	}
	before := selectionCalls(t, e)
	if _, err := e.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err == nil || !strings.Contains(err.Error(), "selection") {
		t.Fatalf("got = %v", err)
	}
	if got, err := e.GetExecution(record.ID); err == nil || got.ID != "" {
		t.Fatalf("got = %+v, %v", got, err)
	}
	if calls := selectionCalls(t, e); calls != before {
		t.Fatalf("got = %s, want %s", calls, before)
	}
}

func TestSavedExecution_GraphChangeDoesNotAddNewSuccessors(t *testing.T) {
	e := selectionEngine(t)
	record, err := e.SavePlans(Options{Nodes: []string{"b"}, Downstream: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.BaseDir, "blueprint.hcl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("edge {\n from = node.d\n to = node.q\n}\n")...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := Load(path, exec.Binary(e.Binary), e.Stdout, e.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := changed.ApplySavedPlans(record.ID, Options{AutoApprove: true}); err == nil || !strings.Contains(err.Error(), "graph configuration changed") {
		t.Fatalf("got = %v", err)
	}
	stored, err := e.GetExecution(record.ID)
	if err != nil || len(stored.Nodes) != 3 {
		t.Fatalf("got = %+v, %v", stored, err)
	}
}

func TestApply_SelectionCannotBypassUnrelatedRecovery(t *testing.T) {
	e := selectionEngine(t)
	e, unlock, err := LoadLocked(filepath.Join(e.BaseDir, "blueprint.hcl"), e.Binary, e.Stdout, e.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	session, err := e.beginExecution("apply", []string{"p"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.transition("p", "indeterminate", "", ""); err != nil {
		t.Fatal(err)
	}
	session.close()
	runs, err := e.Apply(Options{Nodes: []string{"b"}, Downstream: true, AutoApprove: true})
	if err == nil || !strings.Contains(err.Error(), "unresolved mutation") || len(runs) != 0 {
		t.Fatalf("got = %+v, %v", runs, err)
	}
	if calls := selectionCalls(t, e); calls != "" {
		t.Fatalf("got = %s", calls)
	}
}

func TestSavedExecution_RemovedNodeStillReportsStoredSelection(t *testing.T) {
	e := selectionEngine(t)
	record, err := e.SavePlans(Options{Nodes: []string{"b"}, Downstream: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.BaseDir, "blueprint.hcl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(data), reviewNode("d"), "", 1)
	body = strings.Replace(body, "edge {\n from = node.c\n to = node.d\n}\n", "", 1)
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := Load(path, e.Binary, e.Stdout, e.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	var scope *graph.Selection
	_, err = changed.ApplySavedPlans(record.ID, Options{AutoApprove: true, OnSelection: func(s *graph.Selection, _ [][]string) { scope = s }})
	if err == nil || !strings.Contains(err.Error(), "removed from graph") || !reflect.DeepEqual(scope, record.Selection) {
		t.Fatalf("got = %+v, %v", scope, err)
	}
}

func TestReviewPlan_SelectionPreservesIndependentFailureHandling(t *testing.T) {
	e := reviewFixture(t, reviewNode("failed")+reviewNode("dependent")+reviewNode("created")+reviewNode("unselected")+"edge {\n from = node.failed\n to = node.dependent\n}\n")
	runs, err := e.ReviewPlan(Options{Nodes: []string{"failed", "created"}, Downstream: true, Parallelism: 2}, false)
	if err == nil {
		t.Fatal("provider failure accepted")
	}
	assertSelectedRuns(t, runs, []string{"created", "failed", "dependent"})
	if runs[0].Status != StatusPlanned || runs[1].Status != StatusFailed || runs[2].Status != StatusNotRun {
		t.Fatalf("got = %+v", runs)
	}
	calls := selectionCalls(t, e)
	if strings.Contains(calls, "unselected ") || strings.Contains(calls, "dependent plan\n") {
		t.Fatalf("got = %s", calls)
	}
}

func TestApply_SelectionReducesIndependentRuntimeCalls(t *testing.T) {
	for _, selected := range []bool{false, true} {
		e := selectionEngine(t)
		opts := Options{AutoApprove: true}
		want := 7
		if selected {
			opts.Nodes = []string{"b"}
			opts.Downstream = true
			want = 3
		}
		runs, err := e.Apply(opts)
		if err != nil {
			t.Fatal(err)
		}
		calls := selectionCalls(t, e)
		plans, inits := strings.Count(calls, " plan\n"), strings.Count(calls, " init\n")
		if len(runs) != want || plans != want || inits != want {
			t.Fatalf("got = %d nodes, %d plans, %d inits; want %d", len(runs), plans, inits, want)
		}
		t.Logf("selected=%t nodes=%d plan=%d init=%d apply=%d", selected, len(runs), plans, inits, strings.Count(calls, " apply\n"))
	}
}
