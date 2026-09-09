package plugin

import (
	"strings"
	"testing"
)

func TestDescriptor_RejectsExternalStaticGate(t *testing.T) {
	d := Descriptor{Name: "policy", Version: "1.0.0", Protocol: ProtocolVersion, Executable: "policy", Features: []Feature{{Name: "guard", Kind: "gate", Effect: "read_only", Events: []string{"graph.validate"}}}}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "static graph gates must be pure") {
		t.Fatalf("got = %v, want rejected static external gate", err)
	}
}
