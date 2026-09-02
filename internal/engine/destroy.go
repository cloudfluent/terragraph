package engine

import (
	"fmt"
	"io"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// Destroy tears down the selected nodes in reverse topological order (downstream first) so a node is never destroyed while something still depends on its outputs.
//
// Nothing has to be invalidated afterwards. Destroy once had to drop the incremental-apply cache entry for everything it tore down, because a stale "unchanged" hit against infrastructure that no longer exists would have been a correctness bug rather than a missed optimization; a later apply now asks Terraform, which plans against real state and sees the resources are gone.
func (e *Engine) Destroy(opts Options) error {
	// Same reason Apply refuses it: concurrent nodes have their output buffered and flushed a node at a time, so terraform's confirmation prompt would be invisible until long after the answer was needed. Checked before taking the run lock, so an unrunnable combination fails immediately instead of after waiting for whatever else holds it.
	if !opts.AutoApprove && opts.parallelism() > 1 {
		return fmt.Errorf("--parallelism %d needs --auto-approve: output from concurrent nodes is buffered, so there is nowhere to ask for approval", opts.parallelism())
	}

	unlock, err := e.lockRun()
	if err != nil {
		return err
	}
	defer unlock()

	e.logger().Info("destroy starting", "node", opts.Node, "parallelism", opts.parallelism(), "autoApprove", opts.AutoApprove)

	return e.runLevels(opts, true, func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, error) {
		// A destroy plan needs the same resolved input values an apply would have used (e.g. a variable feeding a resource's count or for_each), so it's evaluated identically here: every upstream dependency is still standing at this point, since destroy walks the graph in reverse topological order (downstream first).
		vars, err := e.resolveInputs(name, applied)
		if err != nil {
			return nil, err
		}
		varsPath := e.tfVarsPath(name)
		if _, err := exec.WriteTFVars(varsPath, vars); err != nil {
			return nil, err
		}

		r := &exec.Runner{Binary: e.runtimeFor(name), Dir: e.nodeDir(name), DataDir: e.dataDir(name), Env: e.envFor(name), Stdout: out, Stderr: out}
		// Unlike apply, there is no saved plan here for terragraph to ask about itself, so terraform's own confirmation is the approval — and it needs somewhere to read the answer from. Left nil when auto-approving, so an unattended run can never block on a question.
		if !opts.AutoApprove {
			r.Stdin = e.Stdin
		}
		if err := r.Destroy(opts.AutoApprove, exec.VarFileArgs(varsPath, vars)...); err != nil {
			return nil, fmt.Errorf("destroy: %w", err)
		}
		return nil, nil
	}, nil)
}
