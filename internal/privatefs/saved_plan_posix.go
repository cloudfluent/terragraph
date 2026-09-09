//go:build !windows && !darwin

package privatefs

import "os"

// POSIX access ACL entries remain bounded by the mode's group mask, which protectPlanDirectory clears before writing.
func checkPlanACL(_ *os.File) error { return nil }
