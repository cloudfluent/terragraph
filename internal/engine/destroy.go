package engine

import (
	"fmt"
	"io"
	"os"

	"github.com/cloudfluent/terragraph/internal/blueprint"
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

	// Teardown is delete-only, so approve = "all" is the only level that permits it (the same all-gating apply's notPermitted gives destroy actions). An interactive destroy keeps terraform's own confirmation as the human gate — approve governs what may happen *without* someone saying so — but --auto-approve removes that human, so every in-scope node must already have said yes; --approve=all can only fill a gap, never override a node's own declaration. Checked before any lock is taken, so a refusal fails immediately rather than after waiting on whatever holds it.
	if opts.AutoApprove {
		levels, err := e.executionLevels(opts, true)
		if err != nil {
			return err
		}
		for _, level := range levels {
			for _, name := range level {
				if a := e.approveFor(name, opts.Approve); a != blueprint.ApproveAll {
					return fmt.Errorf("destroy: node %s resolves to approve = %q, which does not permit teardown; set approve = \"all\" on the node (or its enclosing use), pass --approve=all for this run only, or run destroy without --auto-approve to approve interactively", name, a)
				}
			}
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
		// Removed however this node exits: the file holds resolved input values in cleartext, and the next run rewrites it from scratch anyway.
		defer func() { _ = os.Remove(varsPath) }()

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
