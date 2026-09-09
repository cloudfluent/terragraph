package plugins

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/cloudfluent/terragraph/plugin"
	hclog "github.com/hashicorp/go-hclog"
	processplugin "github.com/hashicorp/go-plugin"
	"github.com/hashicorp/go-plugin/runner"
)

// Session serializes calls and quarantines fatal or uncertain RPC failures rather than restarting code during a run.
type Session struct {
	gate      chan struct{}
	once      sync.Once
	cancel    context.CancelFunc
	lifetime  context.Context
	relay     *logRelay
	logCancel context.CancelFunc
	logDone   chan struct{}
	client    *processplugin.Client
	rpc       sdk.RPC
	closed    atomic.Bool
}

// Open rechecks executable bytes immediately before launch and never inherits ambient credentials.
func Open(ctx context.Context, p Package, workDir string, environment []string, lifetimeContext ...context.Context) (*Session, error) {
	parentContext := ctx
	if len(lifetimeContext) > 0 {
		parentContext = lifetimeContext[0]
	}
	ctx, startupCancel := context.WithTimeout(ctx, 10*time.Second)
	defer startupCancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.Descriptor.Validate(); err != nil {
		return nil, err
	}
	checksum, err := hex.DecodeString(p.ExecutableDigest)
	if err != nil || len(checksum) != sha256.Size {
		return nil, fmt.Errorf("plugin checksum is invalid; reinstall the package")
	}
	client := processplugin.NewClient(&processplugin.ClientConfig{
		HandshakeConfig: sdk.Handshake, Plugins: sdk.ClientPlugins(),
		AllowedProtocols: []processplugin.Protocol{processplugin.ProtocolGRPC}, AutoMTLS: true,
		SkipHostEnv: true,
		RunnerFunc: func(logger hclog.Logger, metadata *exec.Cmd, socketDir string) (runner.Runner, error) {
			secure := &processplugin.SecureConfig{Checksum: checksum, Hash: sha256.New()}
			valid, err := secure.Check(p.Path)
			if err != nil || !valid {
				return nil, fmt.Errorf("plugin checksum verification failed")
			}
			cmd := exec.Command(p.Path)
			cmd.Dir = workDir
			cmd.Env = append(append([]string{}, environment...), metadata.Env...)
			return newProcessRunner(logger, cmd, socketDir)
		},
		StartTimeout: 10 * time.Second, Logger: hclog.New(&hclog.LoggerOptions{Output: io.Discard}),
		Stderr: io.Discard, SyncStdout: io.Discard, SyncStderr: io.Discard,
	})
	stop := context.AfterFunc(ctx, client.Kill)
	defer stop()
	connection, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, &CallError{Code: "startup_failed", Fatal: true, cause: ctx.Err()}
	}
	raw, err := connection.Dispense("terragraph")
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("plugin protocol negotiation failed; install a compatible version")
	}
	rpc, ok := raw.(sdk.RPC)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("plugin protocol mismatch")
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d, err := rpc.Describe(callCtx)
	a, _ := json.Marshal(d)
	b, _ := json.Marshal(p.Descriptor)
	if err != nil || string(a) != string(b) {
		client.Kill()
		return nil, &CallError{Code: "descriptor_mismatch", Fatal: true, cause: ctx.Err()}
	}
	lifetime, cancel := context.WithCancel(parentContext)
	s := &Session{client: client, rpc: rpc, gate: make(chan struct{}, 1), cancel: cancel, lifetime: lifetime, logDone: make(chan struct{})}
	settings := settingsFor(ctx)
	if settings.alias == "" {
		settings.alias = p.Descriptor.Name
	}
	s.relay = newLogRelay(settings)
	logCtx, logCancel := context.WithCancel(lifetime)
	s.logCancel = logCancel
	stopLogStartup := context.AfterFunc(ctx, logCancel)
	stream, err := rpc.OpenLogs(logCtx)
	stopLogStartup()
	if err != nil || ctx.Err() != nil {
		cancel()
		client.Kill()
		close(s.relay.stop)
		return nil, fmt.Errorf("plugin logging protocol unavailable; rebuild the plugin with a compatible SDK")
	}
	go func() {
		defer close(s.logDone)
		for {
			entry, err := stream.Recv()
			if err != nil {
				if logCtx.Err() == nil {
					s.relay.streamFailed.Store(true)
				}
				return
			}
			s.relay.receive(entry)
		}
	}()
	return s, nil
}

// Call never returns raw transport or provider errors, which can contain secret request values.
func (s *Session) Call(ctx context.Context, request sdk.Request, timeout time.Duration) (sdk.Response, error) {
	if len(request.Event.Plan) > sdk.MaxPlanSize {
		return sdk.Response{}, &CallError{Code: "request_too_large"}
	}
	if timeout <= 0 {
		return sdk.Response{}, fmt.Errorf("plugin call requires a positive timeout")
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	if s.closed.Load() {
		return sdk.Response{}, &CallError{Code: "session_unavailable", Fatal: true}
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-callCtx.Done():
		return sdk.Response{}, &CallError{Code: "queue_cancelled", cause: callCtx.Err()}
	}
	if s.closed.Load() {
		return sdk.Response{}, &CallError{Code: "session_unavailable", Fatal: true}
	}
	if err := callCtx.Err(); err != nil {
		return sdk.Response{}, &CallError{Code: "call_cancelled", cause: err}
	}
	data, err := json.Marshal(requestForLimit(request))
	if err != nil {
		return sdk.Response{}, &CallError{Code: "invalid_request"}
	}
	if len(data) > sdk.MaxMessageSize-1024 {
		return sdk.Response{}, &CallError{Code: "request_too_large"}
	}
	if err := callCtx.Err(); err != nil {
		return sdk.Response{}, &CallError{Code: "call_cancelled", cause: err}
	}
	request.ID = rand.Text()
	if request.Event.Phase == "" {
		switch request.Action {
		case "configure":
			request.Event.Phase = "plugin.configure"
		case "function":
			request.Event.Phase = "config.evaluate"
		}
	}

	request.LogLevel = int(slog.LevelError)
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		if s.relay.settings.logger.Enabled(callCtx, level) {
			request.LogLevel = int(level)
			break
		}
	}
	s.relay.register(request)
	response, err := s.rpc.Invoke(callCtx, request)
	if err != nil {
		failure := &CallError{Code: "transport_failed", Fatal: true, Sent: true, OutcomeUnknown: true}
		switch status.Code(err) {
		case codes.Canceled:
			failure.Code = "call_cancelled"
			failure.cause = context.Canceled
		case codes.DeadlineExceeded:
			failure.Code = "call_timeout"
			failure.cause = context.DeadlineExceeded
		case codes.ResourceExhausted:
			failure.Code = "response_too_large"
		}
		s.Close()
		return sdk.Response{}, failure
	}
	if response.LogsIncomplete {
		s.relay.mu.Lock()
		delete(s.relay.pending, request.ID)
		s.relay.mu.Unlock()
		s.relay.incomplete.Add(1)
	}
	if response.Fault != nil {
		code := response.Fault.Code
		invalid := !publicCode.MatchString(code)
		if invalid {
			code = "invalid_fault"
		}
		failure := &CallError{Code: code, Fatal: response.Fault.Fatal || invalid, Retryable: response.Fault.Retryable, Sent: true, OutcomeUnknown: response.Fault.Fatal}
		if failure.Fatal {
			s.Close()
		}
		return sdk.Response{}, failure
	}
	return response, nil
}

// Close cancels in-flight work without waiting behind the call queue and bounds diagnostic draining.
func (s *Session) Close() {
	s.once.Do(func() {
		s.closed.Store(true)
		// Only completed calls can drain normally; active requests are cancelled before process shutdown.
		flushCtx, flushCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		s.relay.flush(flushCtx)
		flushCancel()
		s.cancel()
		s.logCancel()
		s.client.Kill()
		<-s.logDone
		close(s.relay.stop)
		select {
		case <-s.relay.done:
		case <-time.After(100 * time.Millisecond):
		}
	})
}

func requestForLimit(request sdk.Request) sdk.Request { request.Event.Plan = nil; return request }
