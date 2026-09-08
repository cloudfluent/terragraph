//go:build !windows

package exec

func environmentKeyEqual(a, b string) bool { return a == b }
