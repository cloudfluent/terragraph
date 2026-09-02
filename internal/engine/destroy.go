package engine

import (
	"fmt"
	"io"
	"sync"

	"github.com/cloudfluent/terragraph/internal/cache"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// Destroy tears down the selected nodes in reverse topological order (downstream first) so a node is never destroyed while something still depends on its outputs. Cache entries for whatever was actually destroyed are dropped even on a partial failure: a stale "unchanged" cache hit against infrastructure that no longer exists would be a correctness bug, not just a missed optimization.
func (e *Engine) Destroy(opts Options) error {
	e.logger().Info("destroy starting", "node", opts.Node, "parallelism", opts.parallelism())
	var destroyed []string
	var mu sync.Mutex

	runErr := e.runLevels(opts, true, func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, error) {
		// A destroy plan needs the same resolved input values an apply would have used (e.g. a variable feeding a resource's count or for_each), so it's evaluated identically here: every upstream dependency is still standing at this point, since destroy walks the graph in reverse topological order (downstream first).
		vars, err := e.resolveInputs(name, applied)
		if err != nil {
			return nil, err
		}
		varsPath := e.tfVarsPath(name)
		if _, err := exec.WriteTFVars(varsPath, vars); err != nil {
			return nil, err
		}

		r := &exec.Runner{Binary: e.runtimeFor(name).Binary, Dir: e.nodeDir(name), DataDir: e.dataDir(name), Stdout: out, Stderr: out}
		if err := r.Destroy(opts.AutoApprove, exec.VarFileArgs(varsPath, vars)...); err != nil {
			return nil, fmt.Errorf("destroy: %w", err)
		}
		mu.Lock()
		destroyed = append(destroyed, name)
		mu.Unlock()
		return nil, nil
	}, nil)

	if len(destroyed) > 0 {
		if store, err := cache.Load(e.cachePath()); err == nil {
			for _, name := range destroyed {
				delete(store, name)
			}
			_ = store.Save(e.cachePath())
		}
	}

	return runErr
}
