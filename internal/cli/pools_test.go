package cli

import (
	"strings"
	"testing"
)

func TestPlan_MalformedPoolKeepsStructuredArgumentsDiagnostic(t *testing.T) {
	out, _, err := runCmd(t, "plan", "--pool", "api=0:vpc", "--output", "json")
	if err == nil || !strings.Contains(out, `"code":"invalid_arguments"`) {
		t.Fatalf("got = %s, %v, want argument diagnostic", out, err)
	}
}
