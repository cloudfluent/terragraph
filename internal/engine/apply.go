package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// Apply runs `terraform apply` over the selected nodes in topological order, feeding each node's real outputs into whatever downstream nodes are applied later in the same run.
//
// Whether a node needs applying is Terraform's decision, not terragraph's: every node is planned with -refresh=true and -detailed-exitcode, and a plan reporting no changes skips the apply. Nothing local is consulted first. An earlier version of this kept a content-addressed cache of source files, resolved inputs and execution identity as a prefilter, which was wrong in three separate ways (backend and inherited context missing from the key, drift never refreshed, files read through file()/templatefile() never invalidating) and, once every hit had to be confirmed by a plan anyway, only served to send *misses* straight to apply without one.
//
// When the plan does report changes, that plan is what gets applied (see Runner.PlanChanges/ApplyPlan), so a node refreshes once and the change made is the change that was planned.
func (e *Engine) Apply(opts Options) error {
	e.logger().Info("apply starting", "node", opts.Node, "parallelism", opts.parallelism())

	return e.runLevels(opts, false, func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, error) {
		vars, err := e.resolveInputs(name, applied)
		if err != nil {
			return nil, err
		}

		varsPath := e.tfVarsPath(name)
		if _, err := exec.WriteTFVars(varsPath, vars); err != nil {
			return nil, err
		}
		varFileArgs := exec.VarFileArgs(varsPath, vars)

		r := &exec.Runner{Binary: e.runtimeFor(name), Dir: e.nodeDir(name), DataDir: e.dataDir(name), Env: e.envFor(name), Stdout: out, Stderr: out}
		if err := r.Init(e.Graph.Nodes[name].BackendConfig); err != nil {
			return nil, fmt.Errorf("init: %w", err)
		}

		// The plan is only worth saving if it can be handed to apply, which needs both that terragraph is the one deciding to apply it (Terraform never prompts for a saved plan, and its own prompt is currently the only approval an interactive run gets) and a backend that can write one locally: an enhanced backend runs the plan on HCP and cannot.
		savedPlan := ""
		if opts.AutoApprove && r.SupportsSavedPlan() {
			savedPlan = e.planPath(name)
			if err := os.MkdirAll(filepath.Dir(savedPlan), 0o755); err != nil {
				return nil, fmt.Errorf("creating plan directory: %w", err)
			}
			// Removed however this node exits: the file holds resolved input values in cleartext, and a plan left behind is only ever stale by the next run.
			defer func() { _ = os.Remove(savedPlan) }()
		} else {
			e.logger().Debug("backend cannot save a plan, applying separately", "node", name, "backend", r.BackendType())
		}

		changes, err := r.PlanChanges(savedPlan, varFileArgs...)
		if err != nil {
			return nil, fmt.Errorf("plan: %w", err)
		}
		if !changes {
			e.logger().Debug("plan reports no changes, skipping apply", "node", name)
			_, _ = fmt.Fprintf(out, "node %s: unchanged, skipping apply\n", name)
			outputs, err := r.Outputs()
			if err != nil {
				return nil, fmt.Errorf("plan says unchanged but outputs are unreadable: %w", err)
			}
			return outputs, nil
		}

		if savedPlan != "" {
			if err := r.ApplyPlan(savedPlan); err != nil {
				return nil, fmt.Errorf("apply: %w", err)
			}
		} else if err := r.Apply(opts.AutoApprove, varFileArgs...); err != nil {
			return nil, fmt.Errorf("apply: %w", err)
		}

		outputs, err := r.Outputs()
		if err != nil {
			return nil, fmt.Errorf("reading outputs after apply: %w", err)
		}
		return outputs, nil
	}, nil)
}
