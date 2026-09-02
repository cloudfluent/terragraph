//go:build !windows

package runlock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func lock(f *os.File, nonblocking bool) error {
	flags := syscall.LOCK_EX
	if nonblocking {
		flags |= syscall.LOCK_NB
	}
	err := syscall.Flock(int(f.Fd()), flags)
	if nonblocking && (errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)) {
		return ErrHeld
	}
	if err != nil {
		return fmt.Errorf("acquiring lock: %w", err)
	}
	return nil
}

func unlock(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("releasing lock: %w", err)
	}
	return nil
}
