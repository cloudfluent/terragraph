//go:build windows

package exec

import "strings"

func normalizeEnvKey(name string) string {
	return strings.ToUpper(name)
}
