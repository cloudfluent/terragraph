package engine

import (
	"fmt"
	"io"
	"os"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// Plan runs `terraform plan` over the selected nodes in topological order, returning each node's outcome (see NodeRun). A node downstream of one that has never been applied will fail to resolve its inputs. See the "known limitation" in the project plan: planning a value that doesn't exist yet is inherently impossible when every node is an independent root module.
func (e *Engine) Plan(opts Options) ([]NodeRun, error) {
	unlock, err := e.lockRun()
	if err != nil {
		return nil, err
	}
	defer unlock()

	unlockGraph, err := e.lockGraph()
	if err != nil {
		return nil, err
	}
	defer unlockGraph()

	e.logger().Info("plan starting", "node", opts.Node, "parallelism", opts.parallelism())
	return e.runLevels(opts, false, func(name string, applied map[string]map[string]any, out io.Writer) (map[string]any, string, error) {
		vars, err := e.resolveInputs(name, applied)
		if err != nil {
			return nil, "", err
		}
		nodeDir := e.nodeDir(name)
		varsPath := e.tfVarsPath(name)
		if _, err := exec.WriteTFVars(varsPath, vars); err != nil {
			return nil, "", err
		}
		// Removed however this node exits: the file holds resolved input values in cleartext, and the next run rewrites it from scratch anyway.
		defer func() { _ = os.Remove(varsPath) }()

		r := &exec.Runner{Binary: e.runtimeFor(name), Dir: nodeDir, DataDir: e.dataDir(name), Env: e.envFor(name), Stdout: out, Stderr: out}
		if err := r.Init(e.Graph.Nodes[name].BackendConfig); err != nil {
			return nil, "", fmt.Errorf("init: %w", err)
		}
		if err := r.Plan(exec.VarFileArgs(varsPath, vars)...); err != nil {
			return nil, "", fmt.Errorf("plan: %w", err)
		}
		return nil, StatusPlanned, nil
	}, nil)
}
