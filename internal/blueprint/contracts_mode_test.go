package blueprint

import (
	"strings"
	"testing"
)

// TestParseFile_ContractsModeEnum proves the mode is reviewed configuration with a closed vocabulary: warn and enforce only, unknown values die at parse with the remedy, and absence leaves the mode unset (behaves as warn everywhere it is read).
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
	if bp.ContractMode != "" {
		t.Fatalf("got = %q, want unset mode, which every reader treats as warn", bp.ContractMode)
	}
}

// TestParseFile_ContractsModeLegacyRejected proves legacy is a parse error carrying the remedy: legacy and warn were always one behavior, so an old blueprint is told to drop the word rather than left guessing.
func TestParseFile_ContractsModeLegacyRejected(t *testing.T) {
	path := writeTemp(t, `
contracts { mode = "legacy" }
node "a" { source = "./m" }
`)
	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "mode must be warn or enforce") || !strings.Contains(err.Error(), "legacy and warn were always one behavior") {
		t.Fatalf("got = %v, want legacy rejected with the warn remedy", err)
	}
}

// TestParseFile_DuplicateContractsBlock proves a second contracts block is refused like every other singleton: the mode is the strictness dial, and last-win would be an unreviewed strictness change.
func TestParseFile_DuplicateContractsBlock(t *testing.T) {
	path := writeTemp(t, `
contracts { mode = "warn" }
contracts { mode = "enforce" }
node "a" { source = "./m" }
`)
	if _, err := ParseFile(path); err == nil || !strings.Contains(err.Error(), "duplicate contracts block") {
		t.Fatalf("got = %v, want duplicate contracts block error", err)
	}
}
