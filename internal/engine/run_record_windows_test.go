package engine

import (
	"path/filepath"
	"testing"
)

func TestRunRecord_WindowsProtectsReceiptsBeforeAndAfterReplacement(t *testing.T) {
	e := recordFixture(t)
	if err := e.writeRunRecord("apply", []NodeRun{{Node: "root", Status: StatusRunning}}); err != nil {
		t.Fatal(err)
	}
	path, err := e.recordPath("apply")
	if err != nil {
		t.Fatal(err)
	}
	assertPrivatePlanACL(t, filepath.Dir(path))
	assertPrivatePlanACL(t, path)
	if err := e.writeRunRecord("apply", []NodeRun{{Node: "root", Status: StatusFailed}}); err != nil {
		t.Fatal(err)
	}
	assertPrivatePlanACL(t, path)
}
