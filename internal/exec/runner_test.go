package exec

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRunner_RejectsExplicitManagedDataDirBeforeStartingBinary(t *testing.T) {
	commands := []struct {
		name string
		run  func(*Runner) error
	}{
		{"init", func(r *Runner) error { return r.Init(nil) }},
		{"plan", func(r *Runner) error { return r.Plan() }},
		{"show", func(r *Runner) error { _, err := r.PlanChangeSet("saved.tfplan"); return err }},
		{"output", func(r *Runner) error { _, err := r.Outputs(); return err }},
	}
	for _, command := range commands {
		for _, key := range []string{"TF_DATA_DIR", "tf_data_dir", "Tf_Data_Dir", "TF_DATA_DIR=shared"} {
			t.Run(command.name+"/"+key, func(t *testing.T) {
				dir := t.TempDir()
				r := &Runner{Binary: Binary(filepath.Join(dir, "must-not-start")), Dir: dir, DataDir: filepath.Join(dir, "managed"), Env: map[string]string{key: "shared"}}
				err := command.run(r)
				if err == nil || !strings.Contains(err.Error(), "env."+key) || !strings.Contains(err.Error(), "remove") {
					t.Fatalf("error = %v, want explicit managed env conflict before binary lookup/start", err)
				}
			})
		}
	}
}

func TestRunnerEnv_NoDataDirNoEnvInheritsAsIs(t *testing.T) {
	r := &Runner{}
	if got, err := r.env(); got != nil || err != nil {
		t.Fatalf("env = %v, error = %v, want inherited environment without error", got, err)
	}
}

func TestRunnerEnv_DataDirSetsTFDataDir(t *testing.T) {
	r := &Runner{DataDir: "/tmp/tfdata/vpc"}
	env, err := r.env()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "TF_DATA_DIR=/tmp/tfdata/vpc") {
		t.Fatalf("expected TF_DATA_DIR in env, got %v", env)
	}
}

func TestRunnerEnv_EnvEntriesAppendedSorted(t *testing.T) {
	r := &Runner{Env: map[string]string{"AWS_REGION": "ap-northeast-2", "AWS_PROFILE": "prod"}}
	env, err := r.env()
	if err != nil {
		t.Fatal(err)
	}

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
	env, err := r.env()
	if err != nil {
		t.Fatal(err)
	}

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
