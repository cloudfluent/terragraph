package exec

import (
	"fmt"
	"strings"
)

// RunOperation preserves native streams and exit errors; the engine must first validate the command capability and supply managed preparation.
func (r *Runner) RunOperation(args ...string) error { return r.run(args...) }

// ValidateOperationEnvironment rejects hidden redirection before an operational command can bypass node selection or write native logs outside managed paths.
func (r *Runner) ValidateOperationEnvironment() error {
	env, err := r.env()
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[strings.ToUpper(key)] = value
	}
	for key, value := range values {
		if value != "" && (strings.HasPrefix(key, "TF_CLI_ARGS") || strings.HasPrefix(key, "TF_LOG")) {
			return fmt.Errorf("run requires explicit runtime arguments and managed diagnostics; unset TF_CLI_ARGS and TF_LOG overrides")
		}
	}
	return nil
}
