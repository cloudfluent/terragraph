package engine

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func TestRecoverExecution_RealFreshBackendCache(t *testing.T) {
	binary := os.Getenv("TERRAGRAPH_TEST_RUNTIME")
	if binary == "" {
		t.Skip("set TERRAGRAPH_TEST_RUNTIME for native recovery evidence")
	}
	dir := observationFixture(t, "local")
	e, err := Load(filepath.Join(dir, "blueprint.hcl"), exec.Binary(binary), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(Options{Nodes: []string{"first"}, AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	s, err := e.beginExecution("apply", []string{"first"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.transition("first", "applied", "", ""); err != nil {
		t.Fatal(err)
	}
	id := s.record.ID
	s.close()
	if err := os.RemoveAll(e.dataDir("first")); err != nil {
		t.Fatal(err)
	}
	record, err := e.RecoverExecution(id, true, false, false)
	if err != nil || record.Nodes[0].Phase != "completed" {
		t.Fatalf("got = %+v, %v", record, err)
	}
	if _, err := os.Stat(e.dataDir("first")); !os.IsNotExist(err) {
		t.Fatal("recovery recreated the mutation cache")
	}
}
