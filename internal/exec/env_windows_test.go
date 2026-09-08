package exec

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunnerEnv_ManagedDataDirOverridesInheritedWindowsCaseVariant(t *testing.T) {
	t.Setenv("tf_data_dir", "host-shared")
	var out bytes.Buffer
	r := &Runner{Binary: "cmd.exe", Dir: t.TempDir(), DataDir: "node-managed", Stdout: &out}
	if err := r.run("/d", "/c", "echo", "%TF_DATA_DIR%"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "node-managed" {
		t.Fatalf("subprocess TF_DATA_DIR = %q, want node-managed", got)
	}
}
