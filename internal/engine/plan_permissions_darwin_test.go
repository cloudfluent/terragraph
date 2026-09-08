//go:build darwin

package engine

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApply_RefusesExtendedPlanDirectoryACL(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	dir := filepath.Dir(e.planPath("cached"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := osexec.Command("chmod", "+a", "everyone allow list,search,readattr,readextattr,readsecurity", dir).CombinedOutput(); err != nil {
		t.Fatalf("adding fixture ACL: %v: %s", err, out)
	}
	if _, err := e.Apply(Options{AutoApprove: true}); err == nil || !strings.Contains(err.Error(), "chmod -N") {
		t.Fatalf("got = %v, want extended ACL refusal and remedy", err)
	}
	if _, err := os.Stat(e.planPath("cached")); !os.IsNotExist(err) {
		t.Fatalf("plan created despite extended ACL: %v", err)
	}
}

func TestApply_RefusesExtendedPlanParentACL(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	dir := filepath.Join(e.BaseDir, ".terragraph")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := osexec.Command("chmod", "+a", "everyone allow add_file,add_subdirectory,delete_child", dir).CombinedOutput(); err != nil {
		t.Fatalf("adding fixture ACL: %v: %s", err, out)
	}
	if _, err := e.Apply(Options{AutoApprove: true}); err == nil || !strings.Contains(err.Error(), "chmod -N") {
		t.Fatalf("got = %v, want extended ACL refusal and remedy", err)
	}
}
