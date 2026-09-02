//go:build !windows

package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func writeFakeTerraform(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "terraform-fake")
	script := `#!/bin/sh
case "$1" in
  init)
    if [ -n "${TG_INIT_LOCK_CONTENT:-}" ]; then
      printf '%s\n' "$TG_INIT_LOCK_CONTENT" > .terraform.lock.hcl
    fi
    exit 0
    ;;
  plan)
    printf 'plan\n' >> "$TG_COMMAND_LOG"
    if [ -f "$TG_PLAN_ERROR" ]; then
      exit 1
    fi
    case "${TF_CLI_ARGS:-} ${TF_CLI_ARGS_plan:-}" in
      *-refresh=false*|*-target=*) exit 0 ;;
    esac
    if [ -f managed.out ] && cmp -s payload.txt managed.out; then
      exit 0
    fi
    exit 2
    ;;
  apply)
    printf 'apply\n' >> "$TG_COMMAND_LOG"
    case "${TF_CLI_ARGS:-} ${TF_CLI_ARGS_apply:-}" in
      *-var=payload=version-two*) printf 'version-two\n' > managed.out ;;
      *) cp payload.txt managed.out ;;
    esac
    exit 0
    ;;
  output)
    printf '{"managed":{"value":"ok"}}'
    exit 0
    ;;
esac
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake terraform: %v", err)
	}
	return path
}

func loadCacheTestEngine(t *testing.T) (*Engine, string, string) {
	t.Helper()
	baseDir := t.TempDir()
	moduleDir := filepath.Join(baseDir, "module")
	writeModule(t, moduleDir)
	if err := os.WriteFile(filepath.Join(moduleDir, "payload.txt"), []byte("version-one\n"), 0o644); err != nil {
		t.Fatalf("writing payload: %v", err)
	}
	blueprintPath := writeBlueprint(t, baseDir, `node "cached" { source = "./module" }`)
	commandLog := filepath.Join(baseDir, "commands.log")
	planError := filepath.Join(baseDir, "plan-error")
	t.Setenv("TG_COMMAND_LOG", commandLog)
	t.Setenv("TG_PLAN_ERROR", planError)

	e, err := Load(blueprintPath, exec.Binary(writeFakeTerraform(t, baseDir)), &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return e, moduleDir, commandLog
}

func TestApply_CacheHitRunsPlanAndReconcilesDrift(t *testing.T) {
	e, moduleDir, commandLog := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("unchanged Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("drifted Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("expected drifted output to be recreated: %v", err)
	}

	data, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("reading command log: %v", err)
	}
	if got, want := strings.Count(string(data), "plan\n"), 2; got != want {
		t.Fatalf("plan count = %d, want %d; log:\n%s", got, want, data)
	}
	if got, want := strings.Count(string(data), "apply\n"), 2; got != want {
		t.Fatalf("apply count = %d, want %d; log:\n%s", got, want, data)
	}
}

func TestApply_CacheHitPlanDetectsNonTerraformDependencyChange(t *testing.T) {
	e, moduleDir, _ := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "payload.txt"), []byte("version-two\n"), 0o644); err != nil {
		t.Fatalf("changing payload: %v", err)
	}
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply after dependency change: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(moduleDir, "managed.out"))
	if err != nil {
		t.Fatalf("reading managed output: %v", err)
	}
	if got, want := string(data), "version-two\n"; got != want {
		t.Fatalf("managed output = %q, want %q", got, want)
	}
}

func TestApply_CacheHitPlanErrorFailsWithoutApplying(t *testing.T) {
	e, _, commandLog := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.WriteFile(os.Getenv("TG_PLAN_ERROR"), nil, 0o644); err != nil {
		t.Fatalf("creating plan error marker: %v", err)
	}
	if err := e.Apply(Options{AutoApprove: true}); err == nil {
		t.Fatal("expected cached-hit plan error to fail apply")
	}

	data, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("reading command log: %v", err)
	}
	if got, want := strings.Count(string(data), "apply\n"), 1; got != want {
		t.Fatalf("apply count = %d, want %d; log:\n%s", got, want, data)
	}
}

func TestApply_CacheHitRecordsSourceChangesMadeByInit(t *testing.T) {
	e, _, commandLog := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	t.Setenv("TG_INIT_LOCK_CONTENT", "provider lock update")
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("cache-hit Apply that updates lock file: %v", err)
	}
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply after stable lock file: %v", err)
	}

	data, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("reading command log: %v", err)
	}
	if got, want := strings.Count(string(data), "plan\n"), 2; got != want {
		t.Fatalf("plan count = %d, want %d; log:\n%s", got, want, data)
	}
	if got, want := strings.Count(string(data), "apply\n"), 1; got != want {
		t.Fatalf("apply count = %d, want %d; log:\n%s", got, want, data)
	}
}

func TestApply_CacheHitBypassesCacheWhenPlanScopeOverridesAreSet(t *testing.T) {
	e, moduleDir, commandLog := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	t.Setenv("TF_CLI_ARGS_plan", "-refresh=false -target=unrelated.resource")
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply with plan scope overrides: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("expected drifted output to be recreated: %v", err)
	}

	data, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("reading command log: %v", err)
	}
	if got, want := strings.Count(string(data), "apply\n"), 2; got != want {
		t.Fatalf("apply count = %d, want %d; log:\n%s", got, want, data)
	}
}

func TestApply_CacheHitBypassesCacheWhenApplyArgumentsChange(t *testing.T) {
	e, moduleDir, commandLog := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	t.Setenv("TF_CLI_ARGS_apply", "-var=payload=version-two")
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply with ambient apply arguments: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(moduleDir, "managed.out"))
	if err != nil {
		t.Fatalf("reading managed output: %v", err)
	}
	if got, want := string(data), "version-two\n"; got != want {
		t.Fatalf("managed output = %q, want %q", got, want)
	}
	logData, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("reading command log: %v", err)
	}
	if got, want := strings.Count(string(logData), "apply\n"), 2; got != want {
		t.Fatalf("apply count = %d, want %d; log:\n%s", got, want, logData)
	}
}
