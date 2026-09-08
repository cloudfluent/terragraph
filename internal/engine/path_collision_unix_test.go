//go:build !windows

package engine

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func TestValidate_SymlinkedDataDirectoriesAreRejected(t *testing.T) {
	root := t.TempDir()
	writeModule(t, filepath.Join(root, "a"))
	writeModule(t, filepath.Join(root, "b"))
	path := writeBlueprint(t, root, `
node "a" { source = "./a" }
node "b" { source = "./b" }
`)
	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := os.MkdirAll(e.dataDir("a"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(e.dataDir("a"), e.dataDir("b")); err != nil {
		t.Fatal(err)
	}
	for _, p := range e.Validate() {
		if p.IsError() && strings.Contains(p.Message, "same managed path") {
			return
		}
	}
	t.Fatalf("got = %v, want managed path collision", e.Validate())
}
