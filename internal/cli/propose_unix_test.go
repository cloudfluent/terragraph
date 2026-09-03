//go:build !windows

package cli

import (
	"strings"
	"testing"
)

// TestPropose_CmdSmoke proves the propose command's wiring end to end through the real command tree: observe writes the lock, propose drafts from it on stdout, and without a lock it fails with the remedy.
func TestPropose_CmdSmoke(t *testing.T) {
	bp := writeObserveCmdFixture(t)

	_, _, err := runCmdAt(t, bp, "propose")
	if err == nil || !strings.Contains(err.Error(), "observe first") {
		t.Fatalf("got = %v, want missing-lock error naming the remedy", err)
	}

	if _, _, err := runCmdAt(t, bp, "observe"); err != nil {
		t.Fatalf("observe: %v", err)
	}
	stdout, _, err := runCmdAt(t, bp, "propose")
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if !strings.Contains(stdout, "Draft contracts from terragraph.lock") {
		t.Fatalf("got stdout %q, want the draft header", stdout)
	}
}
