package pathidentity

import (
	"os"
	"syscall"
	"unsafe"
)

var getFileInformationByHandleEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFileInformationByHandleEx")

// NTFS directory case flags can override the volume default, so querying the directory avoids rejecting valid case-sensitive Windows layouts.
func caseInsensitive(dir string) (bool, bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, false, err
	}
	defer f.Close()
	const fileCaseSensitiveInfo = 23
	const fileCSFlagCaseSensitiveDir = 1
	var flags uint32
	ok, _, callErr := getFileInformationByHandleEx.Call(f.Fd(), fileCaseSensitiveInfo, uintptr(unsafe.Pointer(&flags)), unsafe.Sizeof(flags))
	if ok == 0 {
		if callErr == syscall.Errno(87) || callErr == syscall.Errno(50) || callErr == syscall.Errno(1) {
			return false, false, nil
		}
		return false, false, callErr
	}
	return flags&fileCSFlagCaseSensitiveDir == 0, true, nil
}
