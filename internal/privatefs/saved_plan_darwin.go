//go:build darwin

package privatefs

import (
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// macOS extended ACLs can grant access despite mode 0700, so a mode-only permission change must not claim to protect them.
func checkPlanACL(f *os.File) error {
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	// fgetattrlist starts with a uint32 result length and an attrreference containing the extended-security data length.
	var buf [8192]byte
	//nolint:staticcheck // x/sys has no libSystem wrapper for this ACL query; a failed syscall refuses the plan without requiring cgo.
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, f.Fd(), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)
	if errno != 0 {
		return fmt.Errorf("checking %s extended ACL; use a local private checkout with ACL support: %w", f.Name(), errno)
	}
	if binary.LittleEndian.Uint32(buf[:4]) < 12 {
		return fmt.Errorf("checking %s extended ACL: incomplete filesystem response; use a local private checkout", f.Name())
	}
	if binary.LittleEndian.Uint32(buf[8:12]) != 0 {
		return fmt.Errorf("%s has an extended ACL that mode 0700 cannot restrict; remove its ACL with chmod -N or use a private checkout", f.Name())
	}
	return nil
}
