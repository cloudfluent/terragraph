package engine

import (
	"fmt"
	"io"
	"sync"

	"github.com/cloudfluent/terragraph/internal/cache"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// Apply runs `terraform apply` over the selected nodes in topological order, feeding each node's real outputs into whatever downstream nodes are applied later in the same run. A node whose source and resolved inputs are unchanged since its last recorded apply is skipped (its outputs are still read so downstream nodes can wire against them) unless opts.Force is set.
func (e *Engine) Apply(opts Options) error {
	e.logger().Info("apply starting", "node", opts.Node, "parallelism", opts.parallelism(), "force", opts.Force)
	store, err := cache.Load(e.cachePath())
	if err != nil {
		return err
	}
	var storeMu sync.Mutex

	runErr := e.runLevels(opts, false, func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, error) {
		vars, err := e.resolveInputs(name, applied)
		if err != nil {
			return nil, err
		}

		nodeDir := e.nodeDir(name)
		sourceHash, err := cache.HashDir(nodeDir)
		if err != nil {
			return nil, fmt.Errorf("hashing source: %w", err)
		}
		inputHash, err := cache.HashInputs(vars)
		if err != nil {
			return nil, fmt.Errorf("hashing inputs: %w", err)
		}
		combined := cache.Combine(sourceHash, inputHash)

		storeMu.Lock()
		prev, hasPrev := store[name]
		storeMu.Unlock()

		r := &exec.Runner{Binary: e.Binary, Dir: nodeDir, DataDir: e.dataDir(name), Stdout: out, Stderr: out}

		if !opts.Force && hasPrev && prev == combined {
			e.logger().Debug("cache hit, skipping apply", "node", name)
			_, _ = fmt.Fprintf(out, "node %s: unchanged, skipping apply\n", name)
			outputs, err := r.Outputs()
			if err != nil {
				return nil, fmt.Errorf("cache says unchanged but outputs are unreadable (try --force): %w", err)
			}
			return outputs, nil
		}

		if _, err := exec.WriteTFVars(nodeDir, vars); err != nil {
			return nil, err
		}
		if err := r.Init(e.Graph.Nodes[name].BackendConfig); err != nil {
			return nil, fmt.Errorf("init: %w", err)
		}
		if err := r.Apply(opts.AutoApprove); err != nil {
			return nil, fmt.Errorf("apply: %w", err)
		}

		outputs, err := r.Outputs()
		if err != nil {
			return nil, fmt.Errorf("reading outputs after apply: %w", err)
		}

		// Re-hash the source after init/apply rather than reusing the pre-init `combined` computed above: on a first-ever init, Terraform creates .terraform.lock.hcl, which counts toward the source hash. Storing the pre-init value would permanently mismatch every future run's pre-init hash (which would now see that lock file), defeating the cache forever.
		finalSourceHash, err := cache.HashDir(nodeDir)
		if err != nil {
			return nil, fmt.Errorf("hashing source after apply: %w", err)
		}
		finalCombined := cache.Combine(finalSourceHash, inputHash)

		storeMu.Lock()
		store[name] = finalCombined
		storeMu.Unlock()

		return outputs, nil
	}, func() error {
		storeMu.Lock()
		defer storeMu.Unlock()
		return store.Save(e.cachePath())
	})

	return runErr
}
