//go:build !windows

package exec

import (
	"os/exec"
	"syscall"
)

// StartManagedProcess groups helper processes so an RPC deadline cannot leave ordinary child processes running.
func StartManagedProcess(cmd *exec.Cmd) (func(), error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }, nil
}
