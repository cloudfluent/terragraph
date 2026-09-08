//go:build !windows

package engine

import (
	"fmt"
	"os"
	"syscall"
)

// O_NOFOLLOW keeps permission changes on the actual managed directory instead of a symlink target.
func openPlanDirectory(path string, _ bool) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("%s must be a real directory owned by this user; remove symlinks or use a private checkout: %w", path, err)
	}
	return os.NewFile(uintptr(fd), path), nil
}

func checkPlanParent(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("checking plan parent: %w", err)
	}
	if info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("%s must be owned by this user and not writable by other users; use a private checkout or correct its ownership and remove group/other write permissions", f.Name())
	}
	return checkPlanACL(f)
}

func protectPlanDirectory(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%s is not owned by this user; use a private checkout", f.Name())
	}
	if err := checkPlanACL(f); err != nil {
		return err
	}
	return f.Chmod(0700)
}

func createPreparedPlan(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

func createPlanDirectory(path string) error { return os.Mkdir(path, 0700) }
