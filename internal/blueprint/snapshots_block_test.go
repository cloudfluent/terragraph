package blueprint

import (
	"strings"
	"testing"
)

// TestParseFile_SnapshotsBlock proves the snapshots block is a pure opt-in gate: present means the graph opted into output snapshots, absent means nil (byte-identical live-output behavior), a second block is refused like every other singleton, and v1's empty body rejects any attribute so a typo can't silently configure nothing.
func TestParseFile_SnapshotsBlock(t *testing.T) {
	path := writeTemp(t, `
snapshots { }
node "a" { source = "./m" }
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Snapshots == nil || !bp.Snapshots.Enabled {
		t.Fatalf("got = %+v, want non-nil Snapshots with Enabled true", bp.Snapshots)
	}

	path = writeTemp(t, `node "a" { source = "./m" }`)
	bp, err = ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Snapshots != nil {
		t.Fatalf("got = %+v, want nil Snapshots: the graph did not opt in", bp.Snapshots)
	}

	path = writeTemp(t, `
snapshots { }
snapshots { }
node "a" { source = "./m" }
`)
	if _, err := ParseFile(path); err == nil || !strings.Contains(err.Error(), "duplicate snapshots block") {
		t.Fatalf("got = %v, want duplicate snapshots block error", err)
	}

	path = writeTemp(t, `
snapshots { enabled = true }
node "a" { source = "./m" }
`)
	if _, err := ParseFile(path); err == nil || !strings.Contains(err.Error(), "Unsupported argument") {
		t.Fatalf("got = %v, want unsupported-argument error: v1's snapshots body accepts nothing", err)
	}
}
