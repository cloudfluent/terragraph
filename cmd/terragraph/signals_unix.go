//go:build !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// executionContext lets the engine reap its subprocesses and clean managed files before a terminal interrupt or parent-only SIGTERM releases locks.
func executionContext(command string) (context.Context, func()) {
	switch command {
	case "plan", "apply", "destroy", "run", "recover":
	default:
		return context.Background(), func() {}
	}
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
