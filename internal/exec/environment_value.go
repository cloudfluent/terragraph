package exec

import (
	"os"
	"strings"
)

// EnvironmentValue follows the subprocess's override order and host key semantics so workspace identity cannot differ from the actual process.
func (r *Runner) EnvironmentValue(name string) (string, bool, error) {
	env, err := r.env()
	if err != nil {
		return "", false, err
	}
	if env == nil {
		env = os.Environ()
	}
	value, found := "", false
	for _, entry := range env {
		key, item, ok := strings.Cut(entry, "=")
		if ok && environmentKeyEqual(key, name) {
			value, found = item, true
		}
	}
	return value, found, nil
}
