//go:build !windows

package plugins_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/plugins"
)

func TestEvaluation_CleanupFailureRemainsRecoverable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can remove permission-protected test directories")
	}
	dir := installFixture(t, "")
	evaluation, err := plugins.Evaluate(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, ".terragraph", "plugins", "work")
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(parent, entries[0].Name(), "locked")
	if err := os.Mkdir(locked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "receipt"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0700); _ = evaluation.Close() })
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	if err := evaluation.Close(); err == nil || !strings.Contains(err.Error(), "plugin cleanup") {
		t.Fatalf("got = %v, want actionable cleanup failure", err)
	}
	if err := os.Chmod(locked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := evaluation.Close(); err != nil {
		t.Fatal(err)
	}
}
