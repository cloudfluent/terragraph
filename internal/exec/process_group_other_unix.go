//go:build !windows && !linux && !darwin

package exec

import (
	"errors"
	"syscall"
)

// processGroupRunning conservatively waits for reaping on Unix platforms without a process-state implementation.
func processGroupRunning(pgid int) (bool, error) {
	err := syscall.Kill(-pgid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return true, err
}
