package pathidentity

import (
	"path/filepath"
	"testing"
)

func TestSame_WindowsDirectoryCaseRulesAreAvailable(t *testing.T) {
	dir := t.TempDir()
	_, known, err := Same(filepath.Join(dir, "prod"), filepath.Join(dir, "Prod"))
	if err != nil || !known {
		t.Fatalf("got known = %t, err = %v, want available Windows directory case information", known, err)
	}
}
