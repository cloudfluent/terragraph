package exec

import "golang.org/x/sys/unix"

// processGroupRunning excludes Darwin's SZOMB state because an unreaped descendant cannot write state and its reaping belongs to its parent.
func processGroupRunning(pgid int) (bool, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
	if err != nil {
		return false, err
	}
	for _, process := range processes {
		if process.Proc.P_stat != 5 {
			return true, nil
		}
	}
	return false, nil
}
