//go:build !windows

package exec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func retryRunner(t *testing.T, behavior string) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	t.Setenv("TG_RETRY_CALLS", calls)
	path := filepath.Join(dir, "runtime")
	script := `#!/bin/sh
n=0
if [ -f "$TG_RETRY_CALLS" ]; then n=$(cat "$TG_RETRY_CALLS"); fi
n=$((n+1))
printf '%s' "$n" > "$TG_RETRY_CALLS"
` + behavior
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return &Runner{Binary: Binary(path), Dir: dir, OutputRetries: 2}, calls
}

func TestOutputs_RetriesFailedReadAndRetainsMetadata(t *testing.T) {
	r, calls := retryRunner(t, `if [ "$n" -eq 1 ]; then exit 7; fi
printf '%s' '{"value":{"value":1234567890123456789,"sensitive":true}}'
`)
	outputs, err := r.Outputs()
	if err != nil || outputs["value"].Sensitive == nil || !*outputs["value"].Sensitive {
		t.Fatalf("outputs = %+v, error = %v", outputs, err)
	}
	count, err := os.ReadFile(calls)
	if err != nil || string(count) != "2" {
		t.Fatalf("attempts = %s, error = %v, want 2", count, err)
	}
}

func TestOutputs_DoesNotRetryInvalidJSON(t *testing.T) {
	r, calls := retryRunner(t, `printf 'invalid-json'`)
	if _, err := r.Outputs(); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	count, err := os.ReadFile(calls)
	if err != nil || string(count) != "1" {
		t.Fatalf("attempts = %s, error = %v, want 1", count, err)
	}
}

func TestOutputs_RetryBudgetIsBounded(t *testing.T) {
	r, calls := retryRunner(t, `exit 7`)
	if _, err := r.Outputs(); err == nil {
		t.Fatal("failed output accepted")
	}
	count, err := os.ReadFile(calls)
	if err != nil || string(count) != "3" {
		t.Fatalf("attempts = %s, error = %v, want 3", count, err)
	}
}

func TestApplyPlan_NeverUsesOutputRetryBudget(t *testing.T) {
	r, calls := retryRunner(t, `exit 7`)
	if err := r.ApplyPlan("saved.tfplan"); err == nil {
		t.Fatal("failed apply accepted")
	}
	count, err := os.ReadFile(calls)
	if err != nil || string(count) != "1" {
		t.Fatalf("attempts = %s, error = %v, want 1", count, err)
	}
}

func TestOutputs_CancelsRetryDelay(t *testing.T) {
	r, calls := retryRunner(t, `exit 7`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Context = ctx
	done := make(chan error, 1)
	go func() { _, err := r.Outputs(); done <- err }()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if data, err := os.ReadFile(calls); err == nil && strings.TrimSpace(string(data)) == "1" {
			cancel()
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("output did not start")
		}
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
	count, err := os.ReadFile(calls)
	if err != nil || string(count) != "1" {
		t.Fatalf("attempts = %s, error = %v, want 1", count, err)
	}
}
