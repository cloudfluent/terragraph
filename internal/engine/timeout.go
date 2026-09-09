package engine

import (
	"context"
	"fmt"
)

// nodeEngine isolates deadlines so one timed node cannot cancel an independent sibling or the shared journal.
func (e *Engine) nodeEngine(opts Options) (*Engine, context.CancelFunc) {
	if opts.NodeTimeout == 0 {
		return e, func() {}
	}
	node := *e
	var cancel context.CancelFunc
	node.Context, cancel = context.WithTimeout(e.context(), opts.NodeTimeout)
	return &node, cancel
}

// validateTimeout prevents timed mutations from reaching an interactive approval reader.
func (opts Options) validateTimeout(mutating bool) error {
	if opts.NodeTimeout < 0 {
		return WithDiagnostic(fmt.Errorf("--node-timeout must not be negative; use 0 to disable deadlines"), Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "arguments"})
	}
	if mutating && opts.NodeTimeout > 0 && !opts.AutoApprove {
		return WithDiagnostic(fmt.Errorf("node timeouts need --auto-approve; timed mutations cannot wait for interactive confirmation"), Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "arguments"})
	}
	return nil
}
