package exec

import (
	"context"
	"errors"
	"os/exec"
	"sync"
)

// runCommand preserves Windows console process semantics; Unix process-group signal handling must not be assumed to apply to Windows descendants.
func runCommand(ctx context.Context, cmd *exec.Cmd) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if ctx != nil && ctx.Value(credentialLifetimeKey{}) == true {
		terminate, err := StartManagedProcess(cmd)
		if err != nil {
			return err
		}
		var once sync.Once
		stop := func() { once.Do(terminate) }
		cancel := context.AfterFunc(ctx, stop)
		defer cancel()
		defer stop()
		return errors.Join(cmd.Wait(), ctx.Err())
	}
	return cmd.Run()
}
