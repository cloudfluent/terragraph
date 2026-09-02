//go:build !windows

package exec

func normalizeEnvKey(name string) string {
	return name
}
