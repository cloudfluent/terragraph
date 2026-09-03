package blueprint

import (
	"strings"
	"testing"
)

// TestParseFile_ContractsModeEnum proves the mode is reviewed configuration with a closed vocabulary: unknown values die at parse with the remedy, absence means legacy.
func TestParseFile_ContractsModeEnum(t *testing.T) {
	path := writeTemp(t, `
contracts { mode = "enforce" }
node "a" { source = "./m" }
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.ContractMode != "enforce" {
		t.Fatalf("got = %q, want enforce", bp.ContractMode)
	}

	path = writeTemp(t, `
contracts { mode = "strict" }
node "a" { source = "./m" }
`)
	if _, err := ParseFile(path); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("got = %v, want mode enum error", err)
	}

	path = writeTemp(t, `node "a" { source = "./m" }`)
	bp, err = ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.ContractMode != "legacy" && bp.ContractMode != "" {
		t.Fatalf("got = %q, want legacy default", bp.ContractMode)
	}
}
