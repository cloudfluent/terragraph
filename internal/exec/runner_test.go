package exec

import (
	"slices"
	"testing"
)

func TestRunnerEnv_NoDataDirNoEnvInheritsAsIs(t *testing.T) {
	r := &Runner{}
	if got := r.env(); got != nil {
		t.Fatalf("expected nil (inherit os.Environ() as-is), got %v", got)
	}
}

func TestRunnerEnv_DataDirSetsTFDataDir(t *testing.T) {
	r := &Runner{DataDir: "/tmp/tfdata/vpc"}
	env := r.env()
	if !slices.Contains(env, "TF_DATA_DIR=/tmp/tfdata/vpc") {
		t.Fatalf("expected TF_DATA_DIR in env, got %v", env)
	}
}

func TestRunnerEnv_EnvEntriesAppendedSorted(t *testing.T) {
	r := &Runner{Env: map[string]string{"AWS_REGION": "ap-northeast-2", "AWS_PROFILE": "prod"}}
	env := r.env()

	if !slices.Contains(env, "AWS_PROFILE=prod") || !slices.Contains(env, "AWS_REGION=ap-northeast-2") {
		t.Fatalf("expected both env entries present, got %v", env)
	}

	// The two entries should appear in sorted key order, back to back at the end of the slice.
	tail := env[len(env)-2:]
	if tail[0] != "AWS_PROFILE=prod" || tail[1] != "AWS_REGION=ap-northeast-2" {
		t.Fatalf("expected sorted, deterministic order, got %v", tail)
	}
}

func TestRunnerEnv_EnvOverridesInheritedVariable(t *testing.T) {
	t.Setenv("TG_TEST_OVERRIDE_VAR", "original")
	r := &Runner{Env: map[string]string{"TG_TEST_OVERRIDE_VAR": "overridden"}}
	env := r.env()

	// os/exec (and the underlying execve) take the last occurrence of a duplicate key, the same
	// rule DataDir already relies on for TF_DATA_DIR: appending our override after os.Environ()
	// is what makes it win.
	last := -1
	for i, kv := range env {
		if len(kv) >= len("TG_TEST_OVERRIDE_VAR=") && kv[:len("TG_TEST_OVERRIDE_VAR=")] == "TG_TEST_OVERRIDE_VAR=" {
			last = i
		}
	}
	if last == -1 {
		t.Fatalf("expected TG_TEST_OVERRIDE_VAR to be present in env")
	}
	if env[last] != "TG_TEST_OVERRIDE_VAR=overridden" {
		t.Fatalf("expected the last occurrence to be the override, got %q", env[last])
	}
}
