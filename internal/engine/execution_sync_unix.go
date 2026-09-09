//go:build !windows

package engine

// syncExecutionDirectory persists the rename before a caller may start an infrastructure mutation.
func syncExecutionDirectory(path string) error {
	dir, err := openPlanDirectory(path, false)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
