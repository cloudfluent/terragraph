package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/cloudfluent/terragraph/internal/cache"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// Apply runs `terraform apply` over the selected nodes in topological order, feeding each node's real outputs into whatever downstream nodes are applied later in the same run. A node whose local cache identity is unchanged is skipped only when a refreshed plan also reports no changes, unless opts.Force is set. When that plan does report changes, it is the plan that gets applied, so the two never disagree about what the node should look like.
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

		// appliedSavedPlan records that the verification plan below already did the applying, via ApplyPlan. Falling through to r.Apply in that case would re-plan against state the saved plan has just moved on from, which is the double refresh this whole path exists to avoid.
		appliedSavedPlan := false

		if !opts.Force && hasPrev && prev == combined {
			e.logger().Debug("local cache hit, verifying with plan", "node", name)
			if err := r.Init(e.Graph.Nodes[name].BackendConfig); err != nil {
				return nil, fmt.Errorf("init: %w", err)
			}
			initialized = true

			// The plan is only worth saving if it can be handed to apply, and it can only be handed to apply if terragraph is the one deciding to apply it. Without -auto-approve, applying the saved plan would be applying without ever asking: Terraform never prompts for a plan file, and its prompt is currently the only approval an interactive run gets. So that case keeps the two-invocation path until approval has somewhere else to come from.
			savedPlan := ""
			if opts.AutoApprove && r.SupportsSavedPlan() {
				savedPlan = e.planPath(name)
				if err := os.MkdirAll(filepath.Dir(savedPlan), 0o755); err != nil {
					return nil, fmt.Errorf("creating plan directory: %w", err)
				}
				// Removed however this node exits: the file holds resolved input values in cleartext, and a plan left behind is only ever stale by the next run.
				defer func() { _ = os.Remove(savedPlan) }()
			} else if opts.AutoApprove {
				e.logger().Debug("backend cannot save a plan, applying separately", "node", name, "backend", r.BackendType())
			}

			changes, err := r.PlanChanges(savedPlan, varFileArgs...)
			if err != nil {
				return nil, fmt.Errorf("checking cached node plan: %w", err)
			}
			if !changes {
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
			if savedPlan != "" {
				e.logger().Debug("applying the plan that reported the changes", "node", name)
				if err := r.ApplyPlan(savedPlan); err != nil {
					return nil, fmt.Errorf("apply: %w", err)
				}
				appliedSavedPlan = true
			}
		}

		if !initialized {
			if err := r.Init(e.Graph.Nodes[name].BackendConfig); err != nil {
				return nil, fmt.Errorf("init: %w", err)
			}
		}
		if !appliedSavedPlan {
			if err := r.Apply(opts.AutoApprove, varFileArgs...); err != nil {
				return nil, fmt.Errorf("apply: %w", err)
			}
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
