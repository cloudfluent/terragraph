//go:build windows

package exec

import "testing"

func TestHasNonEmptyEnvPrefixWindowsCaseInsensitive(t *testing.T) {
	if !hasNonEmptyEnvPrefix([]string{"tf_cli_args_plan=-refresh=false"}, "TF_CLI_ARGS") {
		t.Fatal("expected lowercase Windows environment key to be detected")
	}
}

func TestHasNonEmptyEnvPrefixWindowsLastValueWinsAcrossCase(t *testing.T) {
	env := []string{
		"TF_CLI_ARGS_plan=-refresh=false",
		"tf_cli_args_PLAN=",
	}
	if hasNonEmptyEnvPrefix(env, "TF_CLI_ARGS") {
		t.Fatal("expected differently-cased empty override to clear the inherited value")
	}
}
