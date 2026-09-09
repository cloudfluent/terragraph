package plugins

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/cloudfluent/terragraph/plugin"
)

type loggingKey struct{}
type logSettings struct {
	logger            *slog.Logger
	invocation, alias string
}

// WithLogger attaches diagnostics before blueprint evaluation, where the first plugin can already fail or be cancelled.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	settings := settingsFor(ctx)
	if logger != nil {
		settings.logger = logger
	}
	return context.WithValue(ctx, loggingKey{}, settings)
}
func settingsFor(ctx context.Context) logSettings {
	if settings, ok := ctx.Value(loggingKey{}).(logSettings); ok {
		return settings
	}
	return logSettings{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), invocation: rand.Text()}
}

// CallError preserves branching information without retaining raw provider or transport error strings.
type CallError struct {
	Code      string
	Fatal     bool
	Retryable bool
	// Sent means handed to the transport, not proof that an external operation executed.
	Sent           bool
	OutcomeUnknown bool
	cause          error
}

func (e *CallError) Error() string {
	return fmt.Sprintf("plugin %s: inspect the plugin configuration and execution outcome before retrying", e.Code)
}
func (e *CallError) Unwrap() error { return e.cause }

var publicCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type logCall struct{ feature, node, phase string }

// logRelay keeps a slow terminal out of the RPC receive loop and binds provenance to host-owned call metadata.
type logRelay struct {
	settings     logSettings
	queue        chan sdk.LogRecord
	stop         chan struct{}
	done         chan struct{}
	mu           sync.Mutex
	calls        map[string]logCall
	pending      map[string]bool
	order        []string
	dropped      atomic.Uint64
	incomplete   atomic.Uint64
	streamFailed atomic.Bool
	window       time.Time
	received     int
}

func newLogRelay(settings logSettings) *logRelay {
	r := &logRelay{settings: settings, queue: make(chan sdk.LogRecord, sdk.LogQueueSize), stop: make(chan struct{}), done: make(chan struct{}), calls: map[string]logCall{}, pending: map[string]bool{}}
	go r.run()
	return r
}
func (r *logRelay) register(request sdk.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[request.ID] = logCall{request.Feature, request.Event.Node, request.Event.Phase}
	r.pending[request.ID] = true
	r.order = append(r.order, request.ID)
	if len(r.order) > 512 {
		delete(r.calls, r.order[0])
		delete(r.pending, r.order[0])
		r.order = r.order[1:]
	}
}
func (r *logRelay) receive(entry sdk.LogRecord) {
	if entry.Complete {
		r.mu.Lock()
		delete(r.pending, entry.CallID)
		r.mu.Unlock()
		r.dropped.Add(entry.Dropped)
		return
	}
	r.dropped.Add(entry.Dropped)
	if !r.settings.logger.Enabled(context.Background(), slog.Level(entry.Level)) {
		return
	}
	now := time.Now()
	if now.Sub(r.window) >= time.Second {
		r.window = now
		r.received = 0
	}
	r.received++
	if r.received > 2000 {
		r.dropped.Add(1)
		return
	}
	entry.Message = sdk.PublicLogText(entry.Message)
	select {
	case r.queue <- entry:
	default:
		r.dropped.Add(1)
	}
}
func (r *logRelay) write(entry sdk.LogRecord) {
	r.mu.Lock()
	call, known := r.calls[entry.CallID]
	r.mu.Unlock()
	if !known {
		r.dropped.Add(1)
		return
	}
	level := slog.Level(entry.Level)
	if level < slog.LevelDebug || level > slog.LevelError {
		r.dropped.Add(1)
		return
	}
	args := []any{"component", "plugin", "plugin", r.settings.alias, "invocation_id", r.settings.invocation, "call_id", entry.CallID, "sequence", entry.Sequence}
	if call.feature != "" {
		args = append(args, "feature", call.feature)
	}
	if call.node != "" {
		args = append(args, "node", call.node)
	}
	if call.phase != "" {
		args = append(args, "phase", call.phase)
	}
	if !entry.Time.IsZero() {
		args = append(args, "emitted_at", entry.Time.Format(time.RFC3339Nano))
	}
	keys := make([]string, 0, len(entry.Fields))
	for key := range entry.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		if i >= 16 {
			break
		}
		args = append(args, "fields."+sdk.PublicLogText(key), sdk.PublicLogText(entry.Fields[key]))
	}
	r.settings.logger.Log(context.Background(), level, entry.Message, args...)
}
func (r *logRelay) run() {
	defer close(r.done)
	for {
		select {
		case entry := <-r.queue:
			r.write(entry)
		case <-r.stop:
			for {
				select {
				case entry := <-r.queue:
					r.write(entry)
				default:
					if count := r.dropped.Load(); count > 0 || r.incomplete.Load() > 0 || r.streamFailed.Load() {
						code := "log_dropped"
						if r.streamFailed.Load() {
							code = "log_stream_failed"
						}
						r.settings.logger.Warn("plugin diagnostics are incomplete", "component", "plugin_host", "plugin", r.settings.alias, "code", code, "count", count, "incomplete_calls", r.incomplete.Load())
					}
					return
				}
			}
		}
	}
}
func (r *logRelay) flush(ctx context.Context) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		r.mu.Lock()
		pending := len(r.pending)
		r.mu.Unlock()
		if pending == 0 {
			return
		}
		select {
		case <-ctx.Done():
			r.incomplete.Add(uint64(pending))
			return
		case <-ticker.C:
		}
	}
}
