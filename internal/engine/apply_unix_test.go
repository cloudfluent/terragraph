//go:build !windows

package engine

import (
	"bytes"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
  destroy)
    printf 'destroy\n' >> "$TG_COMMAND_LOG"
    # Mirrors the real CLI: -input=false does not suppress the confirmation, so without -auto-approve an answer has to arrive on stdin.
    case "$*" in
      *-auto-approve*) rm -f managed.out; exit 0 ;;
    esac
    read -r answer || exit 1
    case "$answer" in
      yes|y) rm -f managed.out; exit 0 ;;
      *) exit 1 ;;
    esac
    ;;
  show)
    # show -json <plan file>: report what that plan does. TG_PLAN_ACTIONS names the actions, so a test can plan a destroy or a replace without a real provider.
    printf '{"resource_changes":[{"address":"fake.managed","change":{"actions":[%s]}}]}' "${TG_PLAN_ACTIONS:-\"create\"}"
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

func loadApplyTestEngine(t *testing.T) (*Engine, string, string) {
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
	e, moduleDir, commandLog := loadApplyTestEngine(t)

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
	e, moduleDir, _ := loadApplyTestEngine(t)

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
	e, _, commandLog := loadApplyTestEngine(t)

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
	e, moduleDir, commandLog := loadApplyTestEngine(t)

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
	e, moduleDir, commandLog := loadApplyTestEngine(t)

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
	e, moduleDir, commandLog := loadApplyTestEngine(t)

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
	e, moduleDir, _ := loadApplyTestEngine(t)

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

// An enhanced backend cannot write a local plan file, so apply refuses rather than applying without the approve gate.
func TestApply_EnhancedBackendIsRefused(t *testing.T) {
	for _, backend := range []string{"remote", "cloud"} {
		t.Run(backend, func(t *testing.T) {
			e, moduleDir, commandLog := loadApplyTestEngine(t)
			t.Setenv("TG_BACKEND_TYPE", backend)

			err := e.Apply(Options{AutoApprove: true})
			if err == nil {
				t.Fatal("expected apply to refuse an enhanced backend")
			}
			for _, want := range []string{backend, "local plan", "s3"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("expected the error to mention %q, got: %v", want, err)
				}
			}
			if _, statErr := os.Stat(filepath.Join(moduleDir, "managed.out")); !os.IsNotExist(statErr) {
				t.Fatalf("expected nothing to be applied, stat error = %v", statErr)
			}

			data, readErr := os.ReadFile(commandLog)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatalf("reading command log: %v", readErr)
			}
			log := string(data)
			if got := strings.Count(log, "plan\n"); got != 0 {
				t.Fatalf("plan count = %d, want 0; log:\n%s", got, log)
			}
			if got := strings.Count(log, "apply\n"); got != 0 {
				t.Fatalf("apply count = %d, want 0; log:\n%s", got, log)
			}
		})
	}
}

// Without --auto-approve terragraph asks about the plan it just showed, then applies exactly that plan.
func TestApply_WithoutAutoApprovePromptsAndApplies(t *testing.T) {
	e, moduleDir, commandLog := loadApplyTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	if err := os.Truncate(commandLog, 0); err != nil {
		t.Fatalf("truncating command log: %v", err)
	}

	out := &bytes.Buffer{}
	e.Stdout = out
	e.Stdin = strings.NewReader("yes\n")
	if err := e.Apply(Options{}); err != nil {
		t.Fatalf("approved Apply: %v", err)
	}

	if !strings.Contains(out.String(), "Apply these changes to node cached?") {
		t.Fatalf("expected an approval prompt, got:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("expected the approved change to be applied: %v", err)
	}
	data, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("reading command log: %v", err)
	}
	if got, want := strings.Count(string(data), "apply-saved\n"), 1; got != want {
		t.Fatalf("saved-plan apply count = %d, want %d; log:\n%s", got, want, data)
	}
}

// Declining stops the whole run, not just this node: everything downstream consumes its outputs.
func TestApply_WithoutAutoApproveDeclineStopsTheRun(t *testing.T) {
	e, moduleDir, commandLog := loadApplyTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	if err := os.Truncate(commandLog, 0); err != nil {
		t.Fatalf("truncating command log: %v", err)
	}

	e.Stdout = &bytes.Buffer{}
	e.Stdin = strings.NewReader("n\n")
	err := e.Apply(Options{})
	if err == nil {
		t.Fatal("expected a declined approval to fail the run")
	}
	if !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(moduleDir, "managed.out")); !os.IsNotExist(statErr) {
		t.Fatalf("expected nothing to be applied, stat error = %v", statErr)
	}
	data, readErr := os.ReadFile(commandLog)
	if readErr != nil {
		t.Fatalf("reading command log: %v", readErr)
	}
	if got := strings.Count(string(data), "apply\n"); got != 0 {
		t.Fatalf("apply count = %d, want 0; log:\n%s", got, data)
	}
}

// A converged graph never asks, so the no-flag run stays usable as a "is everything still applied?" check with no terminal attached.
func TestApply_UnchangedNodeNeverAsksForApproval(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	e.Stdin = nil
	if err := e.Apply(Options{}); err != nil {
		t.Fatalf("unchanged Apply without auto-approve: %v", err)
	}
}

// A node that does have changes, with nothing to read an answer from, fails and says what to pass instead of hanging or applying unasked.
func TestApply_WithoutAutoApproveAndNoInputFails(t *testing.T) {
	e, moduleDir, _ := loadApplyTestEngine(t)

	if err := e.Apply(Options{AutoApprove: true}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := os.Remove(filepath.Join(moduleDir, "managed.out")); err != nil {
		t.Fatalf("removing managed output: %v", err)
	}
	e.Stdin = nil

	err := e.Apply(Options{})
	if err == nil {
		t.Fatal("expected Apply to fail when it cannot ask for approval")
	}
	if !strings.Contains(err.Error(), "--auto-approve") {
		t.Fatalf("expected the error to name --auto-approve, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(moduleDir, "managed.out")); !os.IsNotExist(statErr) {
		t.Fatalf("expected nothing to be applied, stat error = %v", statErr)
	}
}

const (
	applyChildEnv     = "TG_APPLY_CHILD"
	applyChildNodeEnv = "TG_APPLY_CHILD_NODE"
	applyBlueprintEnv = "TG_APPLY_BLUEPRINT"
	applyBinaryEnv    = "TG_APPLY_BINARY"
)

// Two CLI-equivalent processes applying different nodes of the same blueprint must
// not overlap their terraform runs: they share .terragraph/ and, when source is
// reused, the module's .terraform.lock.hcl. flock is per-process, so this has to
// be two real processes, not two goroutines.
func TestApply_ConcurrentProcessesSerialize(t *testing.T) {
	if os.Getenv(applyChildEnv) == "1" {
		runApplyChild(t)
		return
	}

	baseDir := t.TempDir()
	moduleDir := filepath.Join(baseDir, "module")
	writeModule(t, moduleDir)
	blueprintPath := writeBlueprint(t, baseDir, `
node "a" {
  source = "./module"
  backend_config = { path = "state-a.tfstate" }
}
node "b" {
  source = "./module"
  backend_config = { path = "state-b.tfstate" }
}
`)
	busy := filepath.Join(baseDir, "busy")
	overlap := filepath.Join(baseDir, "overlap")
	release := filepath.Join(baseDir, "release")
	binary := writeOverlappingFakeTerraform(t, baseDir)

	startChild := func(node string, stderr io.Writer) *osexec.Cmd {
		cmd := osexec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
		cmd.Env = append(os.Environ(),
			applyChildEnv+"=1",
			applyChildNodeEnv+"="+node,
			applyBlueprintEnv+"="+blueprintPath,
			applyBinaryEnv+"="+binary,
			"TG_BUSY="+busy,
			"TG_OVERLAP="+overlap,
			"TG_RELEASE="+release,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting apply %s: %v", node, err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		return cmd
	}

	cmdA := startChild("a", os.Stderr)
	t.Cleanup(func() { _ = os.WriteFile(release, []byte("1"), 0o644) })
	waitForBusyDir(t, busy)

	var bErr lockedBuffer
	cmdB := startChild("b", &bErr)
	waitForWaitNotice(t, &bErr)
	if err := os.WriteFile(release, []byte("1"), 0o644); err != nil {
		t.Fatalf("releasing first apply: %v", err)
	}

	if err := cmdA.Wait(); err != nil {
		t.Fatalf("apply --node a: %v", err)
	}
	if err := cmdB.Wait(); err != nil {
		t.Fatalf("apply --node b: %v", err)
	}

	if data, err := os.ReadFile(overlap); err == nil && len(data) > 0 {
		t.Fatalf("concurrent processes overlapped their terraform runs:\n%s", data)
	}
}

// In-process --parallelism is one process holding one lock, so independent nodes
// in the same level must still run terraform concurrently.
func TestApply_InProcessParallelismStillConcurrent(t *testing.T) {
	baseDir := t.TempDir()
	moduleDir := filepath.Join(baseDir, "module")
	writeModule(t, moduleDir)
	blueprintPath := writeBlueprint(t, baseDir, `
node "a" {
  source = "./module"
  backend_config = { path = "state-a.tfstate" }
}
node "b" {
  source = "./module"
  backend_config = { path = "state-b.tfstate" }
}
`)
	busy := filepath.Join(baseDir, "busy")
	overlap := filepath.Join(baseDir, "overlap")
	t.Setenv("TG_BUSY", busy)
	t.Setenv("TG_OVERLAP", overlap)

	e, err := Load(blueprintPath, exec.Binary(writeOverlappingFakeTerraform(t, baseDir)), &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := e.Apply(Options{AutoApprove: true, Parallelism: 2}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(overlap); err != nil {
		t.Fatalf("expected in-process --parallelism to overlap independent nodes: %v", err)
	}
}

func runApplyChild(t *testing.T) {
	t.Helper()
	e, unlock, err := LoadLocked(os.Getenv(applyBlueprintEnv), exec.Binary(os.Getenv(applyBinaryEnv)), os.Stdout, os.Stderr)
	if err != nil {
		t.Fatalf("LoadLocked: %v", err)
	}
	defer unlock()
	if err := e.Apply(Options{Node: os.Getenv(applyChildNodeEnv), AutoApprove: true}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func waitForBusyDir(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

const waitNotice = "waiting for another terragraph process using this blueprint to finish"

func waitForWaitNotice(t *testing.T, buf *lockedBuffer) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), waitNotice) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected the waiting process to say so, stderr = %q", buf.String())
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func writeOverlappingFakeTerraform(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "terraform-overlap")
	// mkdir is the atomic test-and-set: a second apply that reaches terraform
	// while the first still holds TG_BUSY records overlap instead of racing a
	// check-then-touch file.
	script := `#!/bin/sh
case "$1" in
  init)
    exit 0
    ;;
  plan)
    planout=''
    for arg in "$@"; do
      case "$arg" in
        -out=*) planout="${arg#-out=}" ;;
      esac
    done
    if [ -n "$planout" ]; then
      echo plan > "$planout"
    fi
    exit 2
    ;;
  apply)
    if ! mkdir "$TG_BUSY" 2>/dev/null; then
      echo overlap >> "$TG_OVERLAP"
    else
      if [ -n "$TG_RELEASE" ]; then
        while [ ! -f "$TG_RELEASE" ]; do
          sleep 0.01
        done
      else
        sleep 0.4
      fi
      rmdir "$TG_BUSY"
    fi
    exit 0
    ;;
  show)
    printf '{"resource_changes":[{"address":"fake.managed","change":{"actions":["create"]}}]}'
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
		t.Fatalf("writing overlapping fake terraform: %v", err)
	}
	return path
}

// Concurrent nodes buffer their output, so there is nowhere to put a prompt.
func TestApply_ParallelismRequiresAutoApprove(t *testing.T) {
	e, _, _ := loadApplyTestEngine(t)

	err := e.Apply(Options{Parallelism: 2})
	if err == nil {
		t.Fatal("expected --parallelism without --auto-approve to be refused")
	}
	if !strings.Contains(err.Error(), "--auto-approve") {
		t.Fatalf("expected the error to name --auto-approve, got: %v", err)
	}
}
