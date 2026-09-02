// Package exec wraps the terraform/tofu CLI as a subprocess and writes the ephemeral variable file terragraph uses to pass values between nodes. It never generates or modifies any .tf file, only a gitignored, engine-managed tfvars file passed explicitly via -var-file (see WriteTFVars/VarFileArgs).
package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
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
