package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func TestRunRecord_ResumeRechecksSuccessfulAncestors(t *testing.T) {
	e := recordFixture(t)
	failure := errors.New("PRIVATE_ERROR_VALUE")
	action := func(_ context.Context, name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		if name == "left" {
			return nil, "", failure
		}
		return exec.Outputs{"secret": {Value: "PRIVATE_OUTPUT_VALUE"}}, StatusApplied, nil
	}
	_, err := e.runLevels(Options{RecordRun: true, operation: "apply", KeepGoing: true, Parallelism: 2}, false, action, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	path, err := e.recordPath("apply")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PRIVATE_") {
		t.Fatal("receipt persisted payload or error text")
	}
	opts, err := e.prepareOptions(Options{Resume: true}, "apply")
	if err != nil {
		t.Fatal(err)
	}
	if !opts.IncludeDependencies || !opts.RecordRun {
		t.Fatalf("resume options = %+v", opts)
	}
	p, err := e.Preview(opts, "apply")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, node := range p.Nodes {
		names = append(names, node.Node)
	}
	if !reflect.DeepEqual(names, []string{"root", "left", "right", "leaf"}) {
		t.Fatalf("resumed nodes = %v, want unfinished nodes and all ancestors", names)
	}
	if _, err := e.prepareOptions(Options{Resume: true, Node: "leaf"}, "apply"); err == nil {
		t.Fatal("resume accepted additional explicit targets")
	}
	e.Graph.Nodes["root"].BackendConfig["path"] = filepath.Join(e.BaseDir, "different.tfstate")
	if _, err := e.prepareOptions(Options{Resume: true}, "apply"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("changed identity error = %v", err)
	}
}

func TestRunRecord_InterruptedActionRemainsUnfinished(t *testing.T) {
	e := recordFixture(t)
	action := func(_ context.Context, name string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		opts, err := e.resumeOptions(Options{Resume: true}, "apply")
		if err != nil {
			t.Error(err)
		} else if len(opts.Nodes) != 1 || opts.Nodes[0] != name {
			t.Errorf("unfinished targets = %v, want %s", opts.Nodes, name)
		}
		return nil, StatusApplied, nil
	}
	_, err := e.runLevels(Options{Node: "other", RecordRun: true, operation: "apply"}, false, action, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.resumeOptions(Options{Resume: true}, "apply"); err == nil || !strings.Contains(err.Error(), "no unfinished") {
		t.Fatalf("completed resume error = %v", err)
	}
}

func TestRunRecord_OptOutWritesNothing(t *testing.T) {
	e := recordFixture(t)
	action := func(_ context.Context, _ string, _ map[string]exec.Outputs, _ io.Writer) (exec.Outputs, string, error) {
		return nil, StatusPlanned, nil
	}
	if _, err := e.runLevels(Options{}, false, action, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.BaseDir, ".terragraph", "runs")); !os.IsNotExist(err) {
		t.Fatalf("opt-out history = %v", err)
	}
}

func TestRunRecord_CorruptReceiptCannotSelectWork(t *testing.T) {
	e := recordFixture(t)
	if err := e.writeRunRecord("apply", []NodeRun{{Node: "leaf", Status: StatusFailed}}); err != nil {
		t.Fatal(err)
	}
	path, err := e.recordPath("apply")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{}"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.resumeOptions(Options{Resume: true}, "apply"); err == nil {
		t.Fatal("trailing data accepted")
	}
}

func TestRunRecord_DestroyResumeStillChecksExcludedConsumers(t *testing.T) {
	e := recordFixture(t)
	if err := e.writeRunRecord("destroy", []NodeRun{{Node: "leaf", Status: StatusDestroyed}, {Node: "left", Status: StatusFailed}}); err != nil {
		t.Fatal(err)
	}
	opts, err := e.prepareOptions(Options{Resume: true}, "destroy")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.checkDestroyScope(opts); err == nil {
		t.Fatal("old destroyed status bypassed current dependency scope check")
	}
}

func recordFixture(t *testing.T) *Engine {
	t.Helper()
	e := selectionFixture(t)
	unlock, err := e.lockRun()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
	return e
}
