package exec

import "testing"

func TestEnvironmentValue_UsesExplicitOverride(t *testing.T) {
	t.Setenv("TF_WORKSPACE", "inherited")
	r := Runner{Env: map[string]string{"TF_WORKSPACE": "node"}}
	value, found, err := r.EnvironmentValue("TF_WORKSPACE")
	if err != nil || !found || value != "node" {
		t.Fatalf("got = %q, %v, %v", value, found, err)
	}
}
