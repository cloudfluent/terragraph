package engine

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/cloudfluent/terragraph/internal/cache"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// Apply runs `terraform apply` over the selected nodes in topological order, feeding each node's real outputs into whatever downstream nodes are applied later in the same run. A node whose local cache identity is unchanged is skipped only when a refreshed plan also reports no changes, unless opts.Force is set.
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
		rt := e.runtimeFor(name)
		env := e.envFor(name)
		executionIdentity := rt.cacheIdentity() + "\x00" + envIdentity(env)
		sourceHash, err := cache.HashDir(nodeDir)
		if err != nil {
			return nil, fmt.Errorf("hashing source: %w", err)
		}
		inputHash, err := cache.HashInputs(vars)
		if err != nil {
			return nil, fmt.Errorf("hashing inputs: %w", err)
		}
		combined := cache.Combine(sourceHash, inputHash, executionIdentity)

		storeMu.Lock()
		prev, hasPrev := store[name]
		storeMu.Unlock()

		varsPath := e.tfVarsPath(name)
		if _, err := exec.WriteTFVars(varsPath, vars); err != nil {
			return nil, err
		}
		varFileArgs := exec.VarFileArgs(varsPath, vars)
		r := &exec.Runner{Binary: rt.Binary, Dir: nodeDir, DataDir: e.dataDir(name), Env: env, Stdout: out, Stderr: out}
		initialized := false

		if !opts.Force && hasPrev && prev == combined {
			e.logger().Debug("local cache hit, verifying with plan", "node", name)
			if err := r.Init(e.Graph.Nodes[name].BackendConfig); err != nil {
				return nil, fmt.Errorf("init: %w", err)
			}
			initialized = true
			changes, err := r.PlanChanges(varFileArgs...)
			if errors.Is(err, exec.ErrUnsafeCachePlan) {
				e.logger().Debug("cache validation bypassed by TF_CLI_ARGS override", "node", name)
			} else if err != nil {
				return nil, fmt.Errorf("checking cached node plan: %w", err)
			} else if !changes {
				e.logger().Debug("plan confirmed cache hit, skipping apply", "node", name)
				_, _ = fmt.Fprintf(out, "node %s: unchanged, skipping apply\n", name)
				outputs, err := r.Outputs()
				if err != nil {
					return nil, fmt.Errorf("plan says unchanged but outputs are unreadable (try --force): %w", err)
				}
				finalSourceHash, err := cache.HashDir(nodeDir)
				if err != nil {
					return nil, fmt.Errorf("hashing source after plan: %w", err)
				}
				storeMu.Lock()
				store[name] = cache.Combine(finalSourceHash, inputHash, executionIdentity)
				storeMu.Unlock()
				return outputs, nil
			}
		}

		if !initialized {
			if err := r.Init(e.Graph.Nodes[name].BackendConfig); err != nil {
				return nil, fmt.Errorf("init: %w", err)
			}
		}
		if err := r.Apply(opts.AutoApprove, varFileArgs...); err != nil {
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
		finalCombined := cache.Combine(finalSourceHash, inputHash, executionIdentity)

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
