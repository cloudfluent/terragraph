//go:build !windows

package pathidentity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSame_MissingFileUnderSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	dir, alias := filepath.Join(root, "dir"), filepath.Join(root, "alias")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	same, known, err := Same(filepath.Join(dir, "nested", "state"), filepath.Join(alias, "nested", "state"))
	if err != nil || !known || !same {
		t.Fatalf("got = %t, %t, %v, want true, true, nil", same, known, err)
	}
}

func TestSame_DanglingSymlinkStaysUnknown(t *testing.T) {
	root := t.TempDir()
	target, alias := filepath.Join(root, "target"), filepath.Join(root, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	same, known, err := Same(target, alias)
	if err != nil || known || same {
		t.Fatalf("got = %t, %t, %v, want false, false, nil", same, known, err)
	}
}
