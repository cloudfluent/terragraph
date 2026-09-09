package exec

import (
	"context"
	"os/exec"
)

// runCommand preserves Windows console process semantics; Unix process-group signal handling must not be assumed to apply to Windows descendants.
func runCommand(ctx context.Context, cmd *exec.Cmd) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if ctx != nil {
		if _, bounded := ctx.Deadline(); bounded {
			return runWithDeadline(ctx, cmd)
		}
	}
	return cmd.Run()
}
