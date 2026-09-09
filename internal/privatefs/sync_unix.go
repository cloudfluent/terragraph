//go:build !windows

package privatefs

// SyncDirectory persists the rename before a caller may start an infrastructure mutation.
func SyncDirectory(path string) error {
	dir, err := openPlanDirectory(path, false)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
