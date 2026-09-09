package plugin

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLogger_BoundsAndRedactsBeforeTransmission(t *testing.T) {
	hub := &logHub{queue: make(chan LogRecord, 1)}
	logger := slog.New(&logHandler{hub: hub, callID: "request", level: slog.LevelDebug})
	logger.WithGroup("auth").With("secret", Secret("private-value")).Info("line\n\x1b[31m"+strings.Repeat("x", 10000), "field", strings.Repeat("y", 10000))
	entry := <-hub.queue
	if strings.Contains(entry.Message, "\n") || strings.Contains(entry.Message, "\x1b") || len(entry.Message) > MaxLogText+len("[truncated]") {
		t.Fatal("message not bounded or escaped")
	}
	if entry.Fields["auth.secret"] != "[redacted]" {
		t.Fatalf("got = %v, want redacted fields", entry.Fields)
	}
	if len(entry.Fields["auth.field"]) > MaxLogText+len("[truncated]") {
		t.Fatal("field not bounded")
	}
}

func TestLogger_FullQueueDoesNotBlock(t *testing.T) {
	hub := &logHub{queue: make(chan LogRecord, 1)}
	logger := slog.New(&logHandler{hub: hub, level: slog.LevelDebug})
	logger.Info("first")
	logger.Info("second")
	if hub.dropped.Load() != 1 {
		t.Fatalf("got = %d, want one dropped record", hub.dropped.Load())
	}
}

func TestLogger_MissingContextIsQuiet(t *testing.T) {
	Logger(context.Background()).Info("no transport")
}
