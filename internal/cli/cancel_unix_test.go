//go:build !windows

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPlan_CancellationStopsRuntimeBeforeReturning(t *testing.T) {
	bp := writeRunFixture(t)
	dir := filepath.Dir(bp)
	writeFixtureFile(t, filepath.Join(dir, "terraform-fake"), `#!/bin/sh
[ -f "${0%/*}/stopped" ] && exit 0
trap 'echo stopped > "${0%/*}/stopped"; exit 0' INT TERM
echo $$ > "${0%/*}/started"
while :; do sleep 0.05; done
`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := NewRootCmd("test")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"plan", "--blueprint", bp, "--output", "json"})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(filepath.Join(dir, "started")); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if pid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("runtime did not start")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGTERM) })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want cancellation", err)
		}
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(pid, syscall.SIGTERM)
		<-done
		t.Fatal("cancellation did not stop the runtime")
	}
	if _, err := os.Stat(filepath.Join(dir, "stopped")); err != nil {
		t.Fatal("returned before runtime stopped")
	}
}
