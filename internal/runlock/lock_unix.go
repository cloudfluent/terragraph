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
	// flock is interrupted by signals; retry so a waiter does not fail with EINTR.
	for {
		err := syscall.Flock(int(f.Fd()), flags)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if nonblocking && (errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)) {
			return ErrHeld
		}
		if err != nil {
			return fmt.Errorf("acquiring lock: %w", err)
		}
		return nil
	}
}

func unlock(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("releasing lock: %w", err)
		}
		return nil
	}
}
