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

func TestPlan_CancellationRemovesInspectionArtifact(t *testing.T) {
	bp := writeRunFixture(t)
	dir := filepath.Dir(bp)
	writeFixtureFile(t, filepath.Join(dir, "terraform-fake"), `#!/bin/sh
case "$1" in
init) exit 0 ;;
plan)
 trap 'exit 0' INT TERM
 for arg in "$@"; do
  case "$arg" in -out=*) printf 'SENSITIVE_PLAN' > "${arg#-out=}" ;; esac
 done
 echo ready > "${0%/*}/started"
 while :; do sleep 0.05; done
 ;;
*) exit 1 ;;
esac
`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := NewRootCmd("test")
	var out, diagnostics bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&diagnostics)
	root.SetArgs([]string{"plan", "--blueprint", bp, "--output", "json"})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		cancel()
		<-done
		t.Fatal("inspection plan did not start")
	}
	plan := filepath.Join(dir, ".terragraph", "plans", "a.tfplan")
	if data, err := os.ReadFile(plan); err != nil || string(data) != "SENSITIVE_PLAN" {
		cancel()
		<-done
		t.Fatalf("plan = %q, %v", data, err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got = %v", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("cancelled inspection did not stop")
	}
	if _, err := os.Stat(plan); !os.IsNotExist(err) {
		t.Fatalf("plan remains after cancellation: %v", err)
	}
	if !strings.Contains(out.String(), "\"code\":\"cancelled\"") {
		t.Fatalf("missing cancellation diagnostic: %s", out.String())
	}
}
