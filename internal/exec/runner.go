// Package exec wraps the terraform/tofu CLI as a subprocess and writes the ephemeral variable file terragraph uses to pass values between nodes. It never generates or modifies any .tf file, only a gitignored, engine-managed tfvars file passed explicitly via -var-file (see WriteTFVars/VarFileArgs).
package exec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
)

// Binary selects which CLI terragraph shells out to.
type Binary string

const (
	Terraform Binary = "terraform"
	OpenTofu  Binary = "tofu"
)

// Runner executes one binary against one node's working directory.
type Runner struct {
	Binary Binary
	Dir    string
	// DataDir, if set, becomes TF_DATA_DIR: it isolates where Terraform keeps .terraform/ (downloaded providers and, critically, its cached backend configuration) away from Dir. Without this, two nodes that reuse the same module Source but configure different backend_config would collide: Terraform stores which backend it was last configured with inside .terraform/, keyed by working directory, so the second node's init would fail with "Backend configuration changed" even though -backend-config correctly gave it its own state. DataDir sidesteps that by giving every node its own .terraform/ regardless of whether Dir is shared.
	DataDir string
	// Env, if set, adds extra environment variables (e.g. AWS_PROFILE for a per-node provider configuration; see blueprint.Node.Env) on top of the process's own environment. A key here overrides any same-named variable already inherited, the same "last one wins" rule DataDir already relies on for TF_DATA_DIR.
	Env    map[string]string
	Stdout io.Writer
	Stderr io.Writer
}

func (r *Runner) env() []string {
	if r.DataDir == "" && len(r.Env) == 0 {
		return nil // nil -> os/exec inherits os.Environ() as-is
	}

	env := os.Environ()
	if r.DataDir != "" {
		env = append(env, "TF_DATA_DIR="+r.DataDir)
	}

	// Sorted so the resulting environment is deterministic across runs, matching Init's own reason for sorting backend-config flags: same inputs, same generated command every time.
	keys := make([]string, 0, len(r.Env))
	for k := range r.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+r.Env[k])
	}
	return env
}

func (r *Runner) run(args ...string) error {
	cmd := osexec.Command(string(r.Binary), args...)
	cmd.Dir = r.Dir
	cmd.Env = r.env()
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	return cmd.Run()
}

// Init runs `terraform init`. backendConfig entries are passed as -backend-config=key=value flags (Terraform's partial backend configuration mechanism), which lets the same module be reused by multiple nodes with distinct backend settings (e.g. state file path) without generating or editing any .tf file. A nil/empty map passes no such flags, leaving the module's own backend configuration as-is.
func (r *Runner) Init(backendConfig map[string]string) error {
	args := []string{"init", "-input=false"}
	// Sort keys so the generated flag order is deterministic (stable command lines across runs, easier to diff in logs).
	keys := make([]string, 0, len(backendConfig))
	for k := range backendConfig {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, fmt.Sprintf("-backend-config=%s=%s", k, backendConfig[k]))
	}
	return r.run(args...)
}

func (r *Runner) Plan(extraArgs ...string) error {
	args := append([]string{"plan", "-input=false"}, extraArgs...)
	return r.run(args...)
}

// PlanChanges runs a refresh-enabled plan and distinguishes Terraform's detailed exit codes: zero means no changes, two means changes are present, and every other failure remains an error.
//
// planPath, if non-empty, is passed as -out, so the plan this verdict is based on can be handed straight to ApplyPlan. That, not any inspection of the environment, is what keeps a following apply from describing a different desired configuration than the plan that authorized it: `apply <plan file>` re-reads nothing. -refresh=true stays explicit for the same reason it always did, and is what makes that safe: a command-line flag beats the same flag arriving through TF_CLI_ARGS_plan, so an ambient -refresh=false cannot turn this into a stale-state check that reports "no changes" against infrastructure nobody looked at.
func (r *Runner) PlanChanges(planPath string, extraArgs ...string) (bool, error) {
	args := []string{"plan", "-input=false", "-refresh=true", "-detailed-exitcode"}
	if planPath != "" {
		args = append(args, "-out="+planPath)
	}
	args = append(args, extraArgs...)
	err := r.run(args...)
	if err == nil {
		return false, nil
	}
	var exitErr *osexec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		return true, nil
	}
	return false, err
}

// ApplyPlan applies a plan file previously written by PlanChanges. It deliberately passes no -var-file and no -auto-approve: a saved plan already carries the variable values it was created with (re-supplying them is at best ignored, and an error for -var), and Terraform never asks for approval when applying one, so the decision to call this *is* the approval.
func (r *Runner) ApplyPlan(planPath string) error {
	return r.run("apply", "-input=false", planPath)
}

// BackendType reports the backend a previous Init configured for this node, read from the metadata Terraform writes into its own data directory. An empty string means no backend was recorded, which is the ordinary case for a module that declares no backend block at all (the implicit local backend).
//
// This exists only to tell the two enhanced backends apart from every other one: `remote` and `cloud` run the plan on HCP rather than locally, and cannot write a local plan file for ApplyPlan to consume. Every state-storage backend (s3, gcs, azurerm, http, ...) keeps state remote but runs operations here, so it is indistinguishable from local for this purpose. A read failure is reported as "unknown", not as an error: the caller's fallback is the two-invocation path that worked before saved plans existed, so guessing wrong costs a round trip, never correctness.
func (r *Runner) BackendType() string {
	dir := r.DataDir
	if dir == "" {
		dir = filepath.Join(r.Dir, ".terraform")
	}
	data, err := os.ReadFile(filepath.Join(dir, "terraform.tfstate"))
	if err != nil {
		return ""
	}
	var meta struct {
		Backend struct {
			Type string `json:"type"`
		} `json:"backend"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.Backend.Type
}

// SupportsSavedPlan reports whether this node's backend can write the plan file the saved-plan apply path depends on. See BackendType: only the enhanced backends cannot.
func (r *Runner) SupportsSavedPlan() bool {
	switch r.BackendType() {
	case "remote", "cloud":
		return false
	default:
		return true
	}
}

func (r *Runner) Apply(autoApprove bool, extraArgs ...string) error {
	args := []string{"apply", "-input=false"}
	if autoApprove {
		args = append(args, "-auto-approve")
	}
	args = append(args, extraArgs...)
	return r.run(args...)
}

func (r *Runner) Destroy(autoApprove bool, extraArgs ...string) error {
	args := []string{"destroy", "-input=false"}
	if autoApprove {
		args = append(args, "-auto-approve")
	}
	args = append(args, extraArgs...)
	return r.run(args...)
}

type rawOutput struct {
	Value any `json:"value"`
}

// Outputs runs `terraform output -json` and returns output name -> value. It errors if the node has never been applied (no state / no outputs); callers use that to distinguish "not yet applied" from a real failure.
func (r *Runner) Outputs() (map[string]any, error) {
	var stdout bytes.Buffer
	cmd := osexec.Command(string(r.Binary), "output", "-json")
	cmd.Dir = r.Dir
	cmd.Env = r.env()
	cmd.Stdout = &stdout
	cmd.Stderr = r.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("running %s output -json in %s: %w", r.Binary, r.Dir, err)
	}

	var raw map[string]rawOutput
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return nil, fmt.Errorf("parsing %s output -json in %s: %w", r.Binary, r.Dir, err)
	}

	outputs := make(map[string]any, len(raw))
	for name, o := range raw {
		outputs[name] = o.Value
	}
	return outputs, nil
}
