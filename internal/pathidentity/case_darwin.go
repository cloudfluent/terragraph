package pathidentity

import "syscall"

// Darwin exposes the containing filesystem's actual rule, including case-sensitive APFS volumes, through _PC_CASE_SENSITIVE.
func caseInsensitive(dir string) (bool, bool, error) {
	const pcCaseSensitive = 11
	value, err := syscall.Pathconf(dir, pcCaseSensitive)
	if err == syscall.EINVAL || err == syscall.ENOTSUP {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return value == 0, value >= 0, nil
}
