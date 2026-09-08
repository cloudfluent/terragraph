package pathidentity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSame_MissingCaseVariantsMatchFilesystem(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "absent", "prod"), filepath.Join(root, "absent", "Prod")
	same, known, err := Same(a, b)
	if err != nil {
		t.Fatalf("Same: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(a)); !os.IsNotExist(err) {
		t.Fatalf("comparison created a path: %v", err)
	}
	if err := os.Mkdir(filepath.Dir(a), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, nil, 0600); err != nil {
		t.Fatal(err)
	}
	infoA, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := os.Stat(b)
	observed := err == nil && os.SameFile(infoA, infoB)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if known && same != observed {
		t.Fatalf("got same = %t, want %t from real file lookup", same, observed)
	}
	if !known {
		t.Log("parent case behavior unavailable; comparison correctly retains uncertainty")
	}
}

func TestSame_DifferentMissingPathsRemainDistinct(t *testing.T) {
	root := t.TempDir()
	same, known, err := Same(filepath.Join(root, "a"), filepath.Join(root, "b"))
	if err != nil || !known || same {
		t.Fatalf("got = %t, %t, %v, want false, true, nil", same, known, err)
	}
}

func TestSame_ExistingHardlinks(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := os.WriteFile(a, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	same, known, err := Same(a, b)
	if err != nil || !known || !same {
		t.Fatalf("got = %t, %t, %v, want true, true, nil", same, known, err)
	}
}
