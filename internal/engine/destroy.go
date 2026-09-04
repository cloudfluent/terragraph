package engine

import (
	"fmt"
	"io"
	"sort"
	"strings"

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

	// A --node destroy refuses to strand the graph: while a consumer still reads this node's outputs, tearing the node down first makes the consumer's next resolution die with terraform's misleading "has no output value; apply it first". A full destroy is exempt because reverse topological order (downstream first) destroys the consumers along with it. Checked before any lock is taken so a doomed combination fails immediately.
	if opts.Node != "" {
		var consumers []string
		for _, edge := range e.Graph.Edges {
			if edge.IsDataEdge() && edge.From.Node == opts.Node && edge.To.Node != opts.Node {
				consumers = append(consumers, edge.To.Node)
			}
		}
		if len(consumers) > 0 {
			sort.Strings(consumers)
			return fmt.Errorf("destroy: node %q still feeds %s; destroy consumers first (downstream-to-upstream), remove the edges, or run a full destroy", opts.Node, strings.Join(consumers, ", "))
		}
	}

	unlock, err := e.lockRun()
	if err != nil {
		return err
	}
	defer unlock()

	unlockGraph, err := e.lockGraph()
	if err != nil {
		return err
	}
	defer unlockGraph()

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
		var answered *countingReader
		if !opts.AutoApprove && e.Stdin != nil {
			answered = &countingReader{r: e.Stdin}
			r.Stdin = answered
		}
		if err := r.Destroy(opts.AutoApprove, exec.VarFileArgs(varsPath, vars)...); err != nil {
			// Apply knows in advance whether a node has changes, because it plans first, so it can refuse with noApprovalError before running anything. Destroy has no such plan and cannot tell an unattended no-op (which succeeds, and should) from one about to ask a question nobody can answer. So the hint is attached to the failure rather than predicted.
			//
			// "Was there an answer to be had?" is not the same question as "is Stdin nil?": a CLI run always has one (cmd.InOrStdin()), and redirecting from /dev/null still produces a perfectly good reader that yields nothing. What separates the two is whether terraform got any bytes out of it, which is why this counts them rather than testing for nil.
			if !opts.AutoApprove && (answered == nil || answered.n == 0) {
				return nil, fmt.Errorf("destroy: %w (nothing was available to read approval from; pass --auto-approve to destroy without asking)", err)
			}
			return nil, fmt.Errorf("destroy: %w", err)
		}
		return nil, nil
	}, nil)
}

// countingReader records whether anything was ever read from it, so a failed destroy can tell "nobody answered" from "the answer was rejected". Only ever handed to one subprocess at a time (destroy prompts require --parallelism 1), so it needs no locking.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
