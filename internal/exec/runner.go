// Package exec wraps the terraform/tofu CLI as a subprocess and writes the ephemeral variable file terragraph uses to pass values between nodes. It never generates or modifies any .tf file, only a gitignored, engine-managed tfvars file passed explicitly via -var-file (see WriteTFVars/VarFileArgs).
package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Binary selects which CLI terragraph shells out to.
type Binary string

const (
	Terraform Binary = "terraform"
	OpenTofu  Binary = "tofu"
)

// Runner executes one binary against one node's working directory.
type Runner struct {
	// Context bounds subprocess lifetime so cancellation finishes before the engine releases its run lock.
	Context context.Context
	// OutputRetries only repeats failed output subprocesses; mutating operations and invalid JSON are never retried.
	OutputRetries int
	Binary        Binary
	Dir           string
	// DataDir, if set, becomes TF_DATA_DIR: it isolates where Terraform keeps .terraform/ (downloaded providers and, critically, its cached backend configuration) away from Dir. Without this, two nodes that reuse the same module Source but configure different backend_config would collide: Terraform stores which backend it was last configured with inside .terraform/, keyed by working directory, so the second node's init would fail with "Backend configuration changed" even though -backend-config correctly gave it its own state. DataDir sidesteps that by giving every node its own .terraform/ regardless of whether Dir is shared.
	DataDir string
	// Env overrides inherited variables, but an explicit TF_DATA_DIR (case-insensitive) conflicts with DataDir and fails before execution to prevent nodes sharing a backend cache.
	Env map[string]string
	// Stdin, if set, is handed to the subprocess. Nil leaves it at os/exec's default, the null device, which is what every non-interactive command wants: a terraform/tofu invocation that decides to ask a question there reads EOF and fails rather than hanging forever waiting on a terminal nobody is watching.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ValidateEnv lets graph execution reject explicit managed-variable conflicts before any node runs, including before a live-output error can trigger snapshot fallback.
func (r *Runner) ValidateEnv() error {
	if r.DataDir == "" {
		return nil
	}
	keys := make([]string, 0, len(r.Env))
	for key := range r.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		// The subprocess sees the name before the first equals sign, even when it appeared inside the caller's map key.
		name, _, _ := strings.Cut(key, "=")
		if strings.EqualFold(name, "TF_DATA_DIR") {
			return fmt.Errorf("env.%s: TF_DATA_DIR is managed per node to isolate backend configuration; remove this env entry", key)
		}
	}
	return nil
}

func (r *Runner) env() ([]string, error) {
	if err := r.ValidateEnv(); err != nil {
		return nil, err
	}
	if r.DataDir == "" && len(r.Env) == 0 {
		return nil, nil // nil -> os/exec inherits os.Environ() as-is
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
	return env, nil
}

func (r *Runner) run(args ...string) error {
	env, err := r.env()
	if err != nil {
		return err
	}
	cmd := osexec.Command(string(r.Binary), args...)
	cmd.Dir = r.Dir
	cmd.Env = env
	cmd.Stdin = r.Stdin
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	return runCommand(r.Context, cmd)
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

// ResourceChange is one resource's entry in a saved plan: what Terraform intends to do to it, and to which address.
type ResourceChange struct {
	Address string
	// Actions is Terraform's own vocabulary, verbatim: ["create"], ["update"], ["delete"], ["no-op"], ["read"], or a replacement spelled as ["delete","create"] / ["create","delete"] depending on whether the module asked for create_before_destroy.
	Actions []string
}

// IsReplace reports whether this change destroys and recreates the resource rather than doing one or the other.
func (c ResourceChange) IsReplace() bool {
	var creates, deletes bool
	for _, a := range c.Actions {
		switch a {
		case "create":
			creates = true
		case "delete":
			deletes = true
		}
	}
	return creates && deletes
}

// OutputChange deliberately omits before/after values, which can be sensitive even when resource actions are public.
type OutputChange struct {
	Name    string
	Actions []string
}

// PlanChangeSet reads action metadata from the saved plan; optional output extraction also verifies the JSON format before claiming evidence.
func (r *Runner) PlanChangeSet(planPath string, outputChanges ...*[]OutputChange) ([]ResourceChange, error) {
	env, err := r.env()
	if err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	cmd := osexec.Command(string(r.Binary), "show", "-json", planPath)
	cmd.Dir = r.Dir
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = r.Stderr
	if err := runCommand(r.Context, cmd); err != nil {
		return nil, fmt.Errorf("running %s show -json in %s: %w", r.Binary, r.Dir, err)
	}

	var doc struct {
		FormatVersion string `json:"format_version"`
		OutputChanges map[string]struct {
			Actions []string `json:"actions"`
		} `json:"output_changes"`
		ResourceChanges []struct {
			Address string `json:"address"`
			Change  struct {
				Actions []string `json:"actions"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		return nil, fmt.Errorf("parsing %s show -json in %s: %w", r.Binary, r.Dir, err)
	}

	if len(outputChanges) > 0 {
		if !strings.HasPrefix(doc.FormatVersion, "1.") {
			return nil, fmt.Errorf("unsupported plan JSON format; use a runtime with plan JSON format version 1")
		}
		names := make([]string, 0, len(doc.OutputChanges))
		for name := range doc.OutputChanges {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			*outputChanges[0] = append(*outputChanges[0], OutputChange{Name: name, Actions: doc.OutputChanges[name].Actions})
		}
	}
	changes := make([]ResourceChange, 0, len(doc.ResourceChanges))
	for _, rc := range doc.ResourceChanges {
		changes = append(changes, ResourceChange{Address: rc.Address, Actions: rc.Change.Actions})
	}
	return changes, nil
}

// BackendType reports the backend a previous Init configured for this node, read from the metadata Terraform writes into its own data directory. An empty string means no backend was recorded, which is the ordinary case for a module that declares no backend block at all (the implicit local backend).
//
// This exists only to tell the two enhanced backends apart from every other one: `remote` and `cloud` run the plan on HCP rather than locally, and cannot write a local plan file for ApplyPlan to consume. Every state-storage backend (s3, gcs, azurerm, http, ...) keeps state remote but runs operations here, so it is indistinguishable from local for this purpose. A read failure is reported as empty, not as an error: the caller then treats the node as supporting a saved plan, and a backend that in fact cannot will fail at `plan -out` rather than applying uninspected.
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

func (r *Runner) Destroy(autoApprove bool, extraArgs ...string) error {
	args := []string{"destroy", "-input=false"}
	if autoApprove {
		args = append(args, "-auto-approve")
	}
	args = append(args, extraArgs...)
	return r.run(args...)
}

// Output retains runtime sensitivity because a static module declaration can differ from the files OpenTofu actually executes.
type Output struct {
	Value any `json:"value"`
	// Nil means the runtime omitted sensitivity metadata; absence must never authorize snapshot persistence.
	Sensitive *bool `json:"sensitive"`
}

// Outputs preserves metadata until persistence decisions are made while Values supplies the unchanged inputs passed to downstream nodes.
type Outputs map[string]Output

// Values keeps sensitivity metadata out of downstream tfvars, which must contain only the original payload.
func (outputs Outputs) Values() map[string]any {
	values := make(map[string]any, len(outputs))
	for name, output := range outputs {
		values[name] = output.Value
	}
	return values
}

// Outputs runs `terraform output -json` and preserves sensitivity and exact numbers; an empty collection does not establish deployment history.
func (r *Runner) Outputs() (Outputs, error) {
	if r.OutputRetries < 0 || r.OutputRetries > 10 {
		return nil, fmt.Errorf("output retries must be between 0 and 10")
	}
	ctx := r.Context
	if ctx == nil {
		ctx = context.Background()
	}
	for attempt := 0; ; attempt++ {
		outputs, err := r.outputsOnce()
		var exitErr *osexec.ExitError
		if cancelled := ctx.Err(); cancelled != nil {
			return nil, cancelled
		}
		if err == nil || attempt >= r.OutputRetries || !errors.As(err, &exitErr) {
			return outputs, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *Runner) outputsOnce() (Outputs, error) {
	env, err := r.env()
	if err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	cmd := osexec.Command(string(r.Binary), "output", "-json")
	cmd.Dir = r.Dir
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = r.Stderr
	if err := runCommand(r.Context, cmd); err != nil {
		return nil, fmt.Errorf("running %s output -json in %s: %w", r.Binary, r.Dir, err)
	}

	var raw Outputs
	// Keep number tokens exact so an upstream output is not rounded before becoming a downstream input.
	decoder := json.NewDecoder(&stdout)
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parsing %s output -json in %s: %w", r.Binary, r.Dir, err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("parsing %s output -json in %s: expected a single JSON value", r.Binary, r.Dir)
	}

	return raw, nil
}
