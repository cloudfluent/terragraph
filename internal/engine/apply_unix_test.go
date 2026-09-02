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
	// A stand-in for terraform/tofu that records which subcommands the engine actually invoked and models the parts of the real CLI this package depends on: -detailed-exitcode, -out writing a plan file, and `apply <plan file>` applying what that file recorded rather than re-reading anything.
	script := `#!/bin/sh
case "$1" in
  init)
    if [ -n "${TG_BACKEND_TYPE:-}" ]; then
      mkdir -p "$TF_DATA_DIR"
      printf '{"version":3,"backend":{"type":"%s"}}' "$TG_BACKEND_TYPE" > "$TF_DATA_DIR/terraform.tfstate"
    fi
    exit 0
    ;;
  plan)
    printf 'plan\n' >> "$TG_COMMAND_LOG"
    if [ -f "$TG_PLAN_ERROR" ]; then
      exit 1
    fi
    planout=''
    refresh=''
    for arg in "$@"; do
      case "$arg" in
        -out=*) planout="${arg#-out=}" ;;
        -refresh=true) refresh=explicit ;;
      esac
    done
    if [ -n "$planout" ]; then
      printf 'plan-saved\n' >> "$TG_COMMAND_LOG"
    fi
    ambient="${TF_CLI_ARGS:-} ${TF_CLI_ARGS_plan:-}"
    # An explicit command-line -refresh=true beats the same flag arriving through TF_CLI_ARGS_plan, the way terraform's own last-one-wins argument handling resolves it.
    if [ -z "$refresh" ]; then
      case "$ambient" in *-refresh=false*) exit 0 ;; esac
    fi
    case "$ambient" in *-target=*) exit 0 ;; esac
    if [ -f managed.out ] && cmp -s payload.txt managed.out; then
      exit 0
    fi
    # The desired content is decided here, at plan time, and frozen into the plan file.
    if [ -n "$planout" ]; then
      cp payload.txt "$planout"
    fi
    exit 2
    ;;
  apply)
    printf 'apply\n' >> "$TG_COMMAND_LOG"
    # A bare non-flag argument is a plan file: apply exactly what it recorded, ignoring any ambient overrides, the way a real saved plan does.
    planfile=''
    for arg in "$@"; do
      case "$arg" in -*|apply) ;; *) planfile="$arg" ;; esac
    done
    if [ -n "$planfile" ]; then
      printf 'apply-saved\n' >> "$TG_COMMAND_LOG"
      cp "$planfile" managed.out
      exit 0
    fi
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

func TestApply_PlanDecidesWhetherToApply(t *testing.T) {
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
	if got, want := strings.Count(string(data), "plan\n"), 3; got != want {
		t.Fatalf("plan count = %d, want %d; log:\n%s", got, want, data)
	}
	if got, want := strings.Count(string(data), "apply\n"), 2; got != want {
		t.Fatalf("apply count = %d, want %d; log:\n%s", got, want, data)
	}
}

func TestApply_PlanDetectsNonTerraformDependencyChange(t *testing.T) {
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

func TestApply_PlanErrorFailsWithoutApplying(t *testing.T) {
	e, _, commandLog := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.WriteFile(os.Getenv("TG_PLAN_ERROR"), nil, 0o644); err != nil {
		t.Fatalf("creating plan error marker: %v", err)
	}
	if err := e.Apply(Options{AutoApprove: true}); err == nil {
		t.Fatal("expected a plan error to fail the node instead of applying it")
	}

	data, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("reading command log: %v", err)
	}
	if got, want := strings.Count(string(data), "apply\n"), 1; got != want {
		t.Fatalf("apply count = %d, want %d; log:\n%s", got, want, data)
	}
}

// The verification plan passes -refresh=true on the command line, which beats an ambient TF_CLI_ARGS_plan=-refresh=false. This is what lets the cache-validation guard be deleted rather than narrowed: drift is still found even when the environment asks for a stale-state plan.
func TestApply_VerificationPlanRefreshesDespiteAmbientOverride(t *testing.T) {
	e, moduleDir, commandLog := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	t.Setenv("TF_CLI_ARGS_plan", "-refresh=false")
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply with ambient refresh override: %v", err)
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

// A saved plan carries what it was planned with, so an argument injected into apply alone can no longer make apply do something the plan never described. Previously this case bypassed the cache entirely and let TF_CLI_ARGS_apply win.
func TestApply_SavedPlanIgnoresAmbientApplyArguments(t *testing.T) {
	e, moduleDir, commandLog := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	t.Setenv("TF_CLI_ARGS_apply", "-var=payload=version-two")
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("Apply with ambient apply arguments: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(moduleDir, "managed.out"))
	if err != nil {
		t.Fatalf("reading managed output: %v", err)
	}
	if got, want := string(data), "version-one\n"; got != want {
		t.Fatalf("managed output = %q, want %q (the saved plan's content, not the injected argument's)", got, want)
	}
	logData, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("reading command log: %v", err)
	}
	if got, want := strings.Count(string(logData), "apply-saved\n"), 2; got != want {
		t.Fatalf("saved-plan apply count = %d, want %d; log:\n%s", got, want, logData)
	}
}

// A node with changes refreshes once, not twice: the plan that found the changes is the plan that gets applied.
func TestApply_DriftedNodeAppliesTheVerificationPlan(t *testing.T) {
	e, moduleDir, commandLog := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("drifted Apply: %v", err)
	}

	data, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("reading command log: %v", err)
	}
	log := string(data)
	if got, want := strings.Count(log, "plan-saved\n"), 2; got != want {
		t.Fatalf("saved plan count = %d, want %d; log:\n%s", got, want, log)
	}
	if got, want := strings.Count(log, "apply-saved\n"), 2; got != want {
		t.Fatalf("saved-plan apply count = %d, want %d; log:\n%s", got, want, log)
	}
}

// The plan file holds resolved input values in cleartext, so it must not survive the run that produced it.
func TestApply_SavedPlanIsRemovedAfterTheRun(t *testing.T) {
	e, moduleDir, _ := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("drifted Apply: %v", err)
	}

	if _, err := os.Stat(e.planPath("cached")); !os.IsNotExist(err) {
		t.Fatalf("expected the saved plan to be removed, stat error = %v", err)
	}
}

// An enhanced backend cannot write a local plan file, so those nodes keep the two-invocation path instead of failing.
func TestApply_EnhancedBackendSkipsTheSavedPlan(t *testing.T) {
	e, moduleDir, commandLog := loadCacheTestEngine(t)
	t.Setenv("TG_BACKEND_TYPE", "remote")

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
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
	if got := strings.Count(string(data), "plan-saved\n"); got != 0 {
		t.Fatalf("saved plan count = %d, want 0; log:\n%s", got, data)
	}
	if got, want := strings.Count(string(data), "apply\n"), 2; got != want {
		t.Fatalf("apply count = %d, want %d; log:\n%s", got, want, data)
	}
}

// Without -auto-approve there is nothing standing in for approval once a saved plan is applied (terraform never prompts for one), so that path is deliberately left alone until #29 gives approval somewhere to come from.
func TestApply_WithoutAutoApproveDoesNotSaveAPlan(t *testing.T) {
	e, moduleDir, commandLog := loadCacheTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	// Only the run below is under test; the bootstrap above legitimately saved a plan.
	if err := os.Truncate(commandLog, 0); err != nil {
		t.Fatalf("truncating command log: %v", err)
	}
	if err := e.Apply(Options{}); err != nil {
		t.Fatalf("Apply without auto-approve: %v", err)
	}

	data, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("reading command log: %v", err)
	}
	if got := strings.Count(string(data), "plan-saved\n"); got != 0 {
		t.Fatalf("saved plan count = %d, want 0; log:\n%s", got, data)
	}
}
