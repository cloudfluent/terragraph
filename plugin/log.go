package plugin

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

const LogQueueSize = 256
const MaxLogText = 2048

// LogRecord carries bounded public diagnostics separately from feature responses and never controls execution policy.
type LogRecord struct {
	Complete bool              `json:"complete,omitempty"`
	Time     time.Time         `json:"time"`
	Level    int               `json:"level"`
	Message  string            `json:"message"`
	Fields   map[string]string `json:"fields,omitempty"`
	CallID   string            `json:"call_id"`
	Sequence uint64            `json:"sequence"`
	Dropped  uint64            `json:"dropped,omitempty"`
}

// LogStream keeps progress visible while a feature is still running or fails before returning a response.
type LogStream interface{ Recv() (LogRecord, error) }

type loggerKey struct{}

var quietLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// Logger can also be passed to libraries accepting slog, preserving the originating request's identity.
func Logger(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return quietLogger
}

// Secret discards the original value before a diagnostic enters the queue or crosses the process boundary.
func Secret(_ any) slog.Value { return slog.StringValue("[redacted]") }

// PublicLogText bounds and escapes control characters so messages cannot forge terminal lines or consume unbounded queue memory.
func PublicLogText(value string) string {
	if len(value) > MaxLogText {
		value = value[:MaxLogText] + "[truncated]"
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
}

type logHub struct {
	queue    chan LogRecord
	sequence atomic.Uint64
	dropped  atomic.Uint64
}

type logHandler struct {
	hub    *logHub
	callID string
	level  slog.Level
	fields map[string]string
	group  string
}

func (h *logHandler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }
func (h *logHandler) Handle(_ context.Context, record slog.Record) error {
	fields := map[string]string{}
	for k, v := range h.fields {
		fields[k] = v
	}
	record.Attrs(func(attr slog.Attr) bool { h.add(fields, h.group, attr); return len(fields) < 16 })
	entry := LogRecord{Time: record.Time.UTC(), Level: int(record.Level), Message: PublicLogText(record.Message), Fields: fields, CallID: h.callID, Sequence: h.hub.sequence.Add(1)}
	select {
	case h.hub.queue <- entry:
	default:
		h.hub.dropped.Add(1)
	}
	return nil
}
func (h *logHandler) add(fields map[string]string, prefix string, attr slog.Attr) {
	if len(fields) >= 16 {
		return
	}
	value := attr.Value.Resolve()
	key := prefix + PublicLogText(attr.Key)
	if value.Kind() == slog.KindGroup {
		for _, child := range value.Group() {
			h.add(fields, key+".", child)
		}
		return
	}
	fields[PublicLogText(key)] = PublicLogText(value.String())
}
func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.fields = map[string]string{}
	for k, v := range h.fields {
		clone.fields[k] = v
	}
	for _, attr := range attrs {
		clone.add(clone.fields, h.group, attr)
	}
	return &clone
}
func (h *logHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.group = PublicLogText(h.group + name + ".")
	return &clone
}
