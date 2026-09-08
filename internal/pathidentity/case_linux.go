//go:build linux && (amd64 || arm64)

package pathidentity

import (
	"os"
	"syscall"
	"unsafe"
)

// ext4 and f2fs expose per-directory casefold flags; other filesystems stay unknown instead of inheriting a Linux-wide assumption.
func caseInsensitive(dir string) (bool, bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = f.Close() }()
	var stat syscall.Statfs_t
	if err := syscall.Fstatfs(int(f.Fd()), &stat); err != nil {
		return false, false, err
	}
	if stat.Type != 0xef53 && stat.Type != 0xf2f52010 {
		return false, false, nil
	}
	const fsIOCGetFlags = 0x80086601
	const fsCasefold = 0x40000000
	var flags int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fsIOCGetFlags, uintptr(unsafe.Pointer(&flags)))
	if errno == syscall.ENOTTY || errno == syscall.EOPNOTSUPP || errno == syscall.EINVAL {
		return false, false, nil
	}
	if errno != 0 {
		return false, false, errno
	}
	return flags&fsCasefold != 0, true, nil
}
