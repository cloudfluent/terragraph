//go:build !darwin && !windows && (!linux || (!amd64 && !arm64))

package pathidentity

func caseInsensitive(string) (bool, bool, error) { return false, false, nil }
