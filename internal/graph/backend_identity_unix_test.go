//go:build !windows

package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

func TestValidate_ImplicitStateThroughSymlinkSourceIsError(t *testing.T) {
	root := t.TempDir()
	writeBackendModule(t, filepath.Join(root, "module"), "")
	if err := os.Symlink(filepath.Join(root, "module"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `
node "a" { source = "./module" }
node "b" { source = "./alias" }
`)
	bp, err := blueprint.ParseFile(filepath.Join(root, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	g, err := Build(bp, root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if problems := Validate(g); !hasErrorContaining(problems, "same local state") {
		t.Fatalf("got = %v, want same local state error", problems)
	}
	if g.Nodes["a"].Dir == g.Nodes["b"].Dir {
		t.Fatal("source identity was rewritten")
	}
}
