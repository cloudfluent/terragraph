//go:build !windows

package exec

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// runCommand owns the entire subprocess group so a terminating wrapper cannot leave Terraform or provider children running beyond the graph lock.
func runCommand(ctx context.Context, cmd *exec.Cmd) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stopInput, terminalInput := prepareCommandInput(ctx, cmd)
	defer stopInput()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	poll := time.NewTicker(25 * time.Millisecond)
	defer poll.Stop()
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	var grace <-chan time.Time
	var waitErr error
	waited := false
	cancelled := false
	cancel := ctx.Done()
	for {
		select {
		case waitErr = <-done:
			waited = true
			done = nil
		default:
		}

		if waited || terminalInput {
			// A wrapper exiting does not prove its children stopped; zombies cannot write state, but every live group member must finish before locks can be released.
			running, err := processGroupRunning(cmd.Process.Pid)
			if err == nil && !running {
				stopInput()
				if !waited {
					terminalInput = false
					continue
				}
				if cancelled {
					return ctx.Err()
				}
				return waitErr
			}
		}
		if !cancelled && ctx.Err() != nil {
			cancelled = true
			cancel = nil
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
			timer = time.NewTimer(5 * time.Second)
			grace = timer.C
		}
		select {
		case waitErr = <-done:
			waited = true
			done = nil
		case <-cancel:
		case <-grace:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			grace = nil
		case <-poll.C:
		}
	}
}
