package cli

import (
	"fmt"
	"io"
	"log/slog"
)

// parseLogLevel maps the --log-level flag's value to a slog.Level. warn is the default, matching today's behavior of the CLI staying silent on stderr unless something actually needs attention.
func parseLogLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q (want \"debug\", \"info\", \"warn\", or \"error\")", s)
	}
}

// newLogger builds the leveled logger used for internal-machinery diagnostics (node dispatch, cache hits, blueprint load steps). It writes to w (always stderr in practice) and is independent of a command's own stdout result, which stays plain text or --output json regardless of --log-level.
func newLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
