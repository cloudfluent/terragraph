package plugins_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudfluent/terragraph/internal/cli"
	"github.com/cloudfluent/terragraph/internal/plugins"
	sdk "github.com/cloudfluent/terragraph/plugin"
	"github.com/zclconf/go-cty/cty"
)

type logCapture struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	changed chan struct{}
}

type blockedLogWriter struct {
	entered, release chan struct{}
	once             sync.Once
}

func (w *blockedLogWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func TestLogging_BlockedWriterDoesNotBlockCallOrClose(t *testing.T) {
	w := &blockedLogWriter{entered: make(chan struct{}), release: make(chan struct{})}
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(w.release) }) })
	ctx := plugins.WithLogger(context.Background(), slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})))
	p, err := plugins.Inspect(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := plugins.Open(ctx, p, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Call(context.Background(), request("logs"), time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("log writer was not called")
	}
	start := time.Now()
	s.Close()
	if time.Since(start) > 4*time.Second {
		t.Fatal("slow log output blocked session shutdown")
	}
	unblock.Do(func() { close(w.release) })
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	n, err := c.buffer.Write(p)
	c.mu.Unlock()
	select {
	case c.changed <- struct{}{}:
	default:
	}
	return n, err
}
func (c *logCapture) String() string { c.mu.Lock(); defer c.mu.Unlock(); return c.buffer.String() }
func (c *logCapture) wait(t *testing.T, message string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if strings.Contains(c.String(), message) {
			return
		}
		select {
		case <-c.changed:
		case <-deadline.C:
			t.Fatalf("got = %s, want live log %q", c.String(), message)
		}
	}
}

func loggedFixture(t *testing.T, level slog.Level) (*plugins.Session, *logCapture) {
	t.Helper()
	output := &logCapture{changed: make(chan struct{}, 1)}
	ctx := plugins.WithLogger(context.Background(), slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: level})))
	p, err := plugins.Inspect(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := plugins.Open(ctx, p, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, output
}

func waitRequest(path string) sdk.Request {
	r := request("wait")
	value, _ := sdk.EncodeValue(cty.StringVal(path), false)
	r.Arguments = []sdk.Value{value}
	return r
}

func TestLogging_StreamsBeforeResponseAndQueuedCancellationPreservesSession(t *testing.T) {
	s, output := loggedFixture(t, slog.LevelInfo)
	release := filepath.Join(t.TempDir(), "release")
	finished := make(chan error, 1)
	go func() { _, err := s.Call(context.Background(), waitRequest(release), 10*time.Second); finished <- err }()
	output.wait(t, "waiting for release")
	select {
	case err := <-finished:
		t.Fatalf("call returned before release: %v", err)
	default:
	}
	start := time.Now()
	_, err := s.Call(context.Background(), request("identity"), 20*time.Millisecond)
	var failure *plugins.CallError
	if !errors.As(err, &failure) || failure.Sent || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("got = %v, want bounded unsent queue cancellation", err)
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Call(context.Background(), request("identity"), time.Second); err != nil {
		t.Fatalf("queue cancellation poisoned session: %v", err)
	}
}

func TestLogging_LevelsRedactionAndHostProvenance(t *testing.T) {
	s, output := loggedFixture(t, slog.LevelInfo)
	if _, err := s.Call(context.Background(), request("logs"), time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()
	text := output.String()
	if !strings.Contains(text, "public progress") || !strings.Contains(text, "plugin=fixture") || !strings.Contains(text, "call_id=") || !strings.Contains(text, "[redacted]") {
		t.Fatalf("got = %s, want attributed redacted logs", text)
	}
	if strings.Contains(text, "debug detail") || strings.Contains(text, "private-secret-token") {
		t.Fatalf("unexpected private or filtered data: %s", text)
	}
}

func TestSession_CloseCancelsActiveCall(t *testing.T) {
	s, output := loggedFixture(t, slog.LevelInfo)
	finished := make(chan error, 1)
	path := filepath.Join(t.TempDir(), "never")
	go func() {
		_, err := s.Call(context.Background(), waitRequest(path), time.Hour)
		finished <- err
	}()
	output.wait(t, "waiting for release")
	start := time.Now()
	s.Close()
	if time.Since(start) > 4*time.Second {
		t.Fatal("close waited for the active call deadline")
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled call succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("active call did not return")
	}
}

func TestSession_PreservesFaultClassification(t *testing.T) {
	s := openFixture(t)
	_, err := s.Call(context.Background(), request("retryable"), time.Second)
	var failure *plugins.CallError
	if !errors.As(err, &failure) || failure.Code != "provider_unavailable" || !failure.Retryable || failure.Fatal || !failure.Sent {
		t.Fatalf("got = %#v, want retryable provider failure", err)
	}
}

func TestSession_OversizedRequestDoesNotPoisonSession(t *testing.T) {
	s := openFixture(t)
	r := request("identity")
	r.Config = map[string]any{"data": strings.Repeat("x", sdk.MaxMessageSize)}
	_, err := s.Call(context.Background(), r, time.Second)
	var failure *plugins.CallError
	if !errors.As(err, &failure) || failure.Code != "request_too_large" || failure.Sent {
		t.Fatalf("got = %v, want pre-send size rejection", err)
	}
	if _, err := s.Call(context.Background(), request("identity"), time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestSession_OversizedResponseHasDistinctCode(t *testing.T) {
	s := openFixture(t)
	_, err := s.Call(context.Background(), request("oversize"), 5*time.Second)
	var failure *plugins.CallError
	if !errors.As(err, &failure) || failure.Code != "response_too_large" || !failure.Fatal {
		t.Fatalf("got = %v, want response size rejection", err)
	}
}

func TestLogging_FloodReportsLossWithoutFailingFunction(t *testing.T) {
	s, output := loggedFixture(t, slog.LevelInfo)
	if _, err := s.Call(context.Background(), request("flood"), 10*time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !strings.Contains(output.String(), "log_dropped") {
		t.Fatal("log flood lost no visible diagnostics")
	}
}

func TestCLI_PluginLogsStayOutOfJSONResult(t *testing.T) {
	dir := installFixture(t, "node \"app\" {\n source = \"./module\"\n vars = { name = test_logs(\"value\") }\n}\n")
	if err := os.Mkdir(filepath.Join(dir, "module"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "module", "main.tf"), []byte("variable \"name\" { type = string }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	stderr := &logCapture{changed: make(chan struct{}, 1)}
	cmd := cli.NewRootCmd("test")
	cmd.SetOut(&stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--blueprint", dir, "--log-level", "info", "validate", "--output", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(stdout.Bytes()) || strings.Contains(stdout.String(), "public progress") || !strings.Contains(stderr.String(), "public progress") || !strings.Contains(stderr.String(), "plugin=test") {
		t.Fatalf("stdout=%s stderr=%s, want separated attributed output", stdout.String(), stderr.String())
	}
}
