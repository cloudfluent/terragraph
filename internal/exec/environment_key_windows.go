//go:build windows

package exec

import "strings"

func environmentKeyEqual(a, b string) bool { return strings.EqualFold(a, b) }
