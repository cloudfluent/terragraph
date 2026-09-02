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
	lock, err := e.lockRun()
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	e.logger().Info("destroy starting", "node", opts.Node, "parallelism", opts.parallelism())

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
		if err := r.Destroy(opts.AutoApprove, exec.VarFileArgs(varsPath, vars)...); err != nil {
			return nil, fmt.Errorf("destroy: %w", err)
		}
		return nil, nil
	}, nil)
}
