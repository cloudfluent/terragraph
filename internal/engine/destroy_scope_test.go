package engine

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func TestDestroyScope_RequiresConsumersOrAcknowledgement(t *testing.T) {
	e, err := Load(filepath.Join("..", "..", "examples", "group", "blueprint.hcl"), exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	opts, err := e.resolveSelection(Options{Nodes: []string{"vpc"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.checkDestroyScope(opts); err == nil || !strings.Contains(err.Error(), "checkout.nodegroup") {
		t.Fatalf("got = %v, want transitive consumer refusal", err)
	}
	opts.AllowOrphanDestroy = true
	if err := e.checkDestroyScope(opts); err != nil {
		t.Fatal(err)
	}
	opts, err = e.resolveSelection(Options{Nodes: []string{"vpc"}, Downstream: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.checkDestroyScope(opts); err != nil {
		t.Fatal(err)
	}
	opts, err = e.resolveSelection(Options{Nodes: []string{"checkout.nodegroup"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.checkDestroyScope(opts); err != nil {
		t.Fatal(err)
	}
}
