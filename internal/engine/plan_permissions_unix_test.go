//go:build !windows

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This checks protection inside the subprocess before its first plan write, so chmod after plan completion cannot satisfy it.
func requirePrivatePlanBeforeWrite(t *testing.T, e *Engine) {
	t.Helper()
	path := string(e.Binary)
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gate := `  plan)
    for arg in "$@"; do
      case "$arg" in
        -out=*)
          plan="${arg#-out=}"
          if [ ! -f "$plan" ] || [ -z "$(find "$plan" -prune -perm 0600)" ] || [ -z "$(find "$(dirname "$plan")" -prune -perm 0700)" ]; then
            printf 'plan is not private before writing\n' >&2
            exit 42
          fi
          ;;
      esac
    done
`
	script = []byte(strings.Replace(string(script), "  plan)\n", gate, 1))
	if err := os.WriteFile(path, script, 0755); err != nil {
		t.Fatal(err)
	}
}

func TestApply_ProtectsPlanBeforeTerraformWrites(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	requirePrivatePlanBeforeWrite(t, e)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(e.planPath("cached")); !os.IsNotExist(err) {
		t.Fatalf("plan remains: %v", err)
	}
}

func TestApply_TightensExistingPlanDirectoryBeforeWriting(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	path := e.planPath("cached")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	requirePrivatePlanBeforeWrite(t, e)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestApply_RefusesSymlinkPlanWithoutChangingTarget(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	path := e.planPath("cached")
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(Options{AutoApprove: true}); err == nil {
		t.Fatal("expected symlink plan refusal")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("got = %q, want keep", got)
	}
}

func TestApply_RefusesSymlinkPlanDirectoryWithoutChangingTarget(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	dir := filepath.Dir(e.planPath("cached"))
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Chmod(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(Options{AutoApprove: true}); err == nil {
		t.Fatal("expected symlink plan directory refusal")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("got target permissions = %o, want 755", info.Mode().Perm())
	}
}

func TestApply_RemovesPreparedPlanAfterPlanningError(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	path := filepath.Join(t.TempDir(), "fail-plan")
	if err := os.WriteFile(path, []byte("fail"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_PLAN_ERROR", path)
	if _, err := e.Apply(Options{AutoApprove: true}); err == nil {
		t.Fatal("expected planning failure")
	}
	if _, err := os.Stat(e.planPath("cached")); !os.IsNotExist(err) {
		t.Fatalf("plan remains: %v", err)
	}
}

func TestApply_RefusesWritablePlanParentWithoutChangingPermissions(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	parent := filepath.Join(e.BaseDir, ".terragraph")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(Options{AutoApprove: true}); err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("got = %v, want unsafe parent refusal", err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0777 {
		t.Fatalf("got permissions = %o, want 777", info.Mode().Perm())
	}
}

func TestApply_ReplacesHardlinkedStalePlanWithoutChangingTarget(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	path := e.planPath("cached")
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, path); err != nil {
		t.Fatal(err)
	}
	requirePrivatePlanBeforeWrite(t, e)
	if _, err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("got = %q, want keep", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("got target permissions = %o, want 644", info.Mode().Perm())
	}
}

func TestApply_RefusesSymlinkPlanParent(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)
	parent := filepath.Join(e.BaseDir, ".terragraph")
	target := t.TempDir()
	if err := os.Symlink(target, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(Options{AutoApprove: true}); err == nil {
		t.Fatal("expected symlink parent refusal")
	}
	if _, err := os.Stat(filepath.Join(target, "plans")); !os.IsNotExist(err) {
		t.Fatalf("plans created in symlink target: %v", err)
	}
}
