//go:build windows

package runlock

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockfileExclusiveLock   = 2
	lockfileFailImmediately = 1
	errorLockViolation      = syscall.Errno(33)
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

func lock(f *os.File, nonblocking bool) error {
	var overlapped syscall.Overlapped
	flags := uint32(lockfileExclusiveLock)
	if nonblocking {
		flags |= lockfileFailImmediately
	}
	r1, _, err := procLockFileEx.Call(
		f.Fd(),
		uintptr(flags),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if r1 == 0 {
		if nonblocking && err == errorLockViolation {
			return ErrHeld
		}
		return fmt.Errorf("acquiring lock: %w", err)
	}
	return nil
}

func unlock(f *os.File) error {
	var overlapped syscall.Overlapped
	r1, _, err := procUnlockFileEx.Call(
		f.Fd(),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if r1 == 0 {
		return fmt.Errorf("releasing lock: %w", err)
	}
	return nil
}
