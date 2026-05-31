// Package log provides the structured (JSON) logger used by GoboxD.
//
// Every log entry is JSON-encoded with an ISO 8601 UTC timestamp at
// millisecond precision under the key "ts" and a level under the key
// "level". Callers attach structured fields with the standard slog API;
// the logger never includes user-submitted source code, stdin, stdout, or
// stderr in any field at INFO level or above.
package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// timestampFormat is the ISO 8601 UTC millisecond format required by the
// architecture spec (REQ A-14.1) and the per-request log entry shape
// (Property 26).
const timestampFormat = "2006-01-02T15:04:05.000Z"

// New constructs a JSON slog.Logger writing to w at the given level.
//
// The "time" attribute slog emits by default is rewritten to a string
// "ts" attribute carrying the ISO 8601 UTC millisecond representation of
// the entry's timestamp. When w is nil, os.Stderr is used.
func New(w io.Writer, level slog.Level) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.String("ts", FormatTime(a.Value.Time()))
			}
			return a
		},
	})
	return slog.New(h)
}

// FormatTime returns t in the ISO 8601 UTC millisecond format used for the
// "ts" log field.
func FormatTime(t time.Time) string {
	return t.UTC().Format(timestampFormat)
}

// ParseLevel parses a LOG_LEVEL string into an slog.Level.
//
// Accepted values (case-insensitive, surrounding whitespace tolerated):
// "DEBUG", "INFO", "WARN" (or "WARNING"), "ERROR". The empty string is
// treated as "INFO". Any other value yields a non-nil error and the
// safe-default INFO level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return slog.LevelDebug, nil
	case "", "INFO":
		return slog.LevelInfo, nil
	case "WARN", "WARNING":
		return slog.LevelWarn, nil
	case "ERROR":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("log: unknown level %q (want DEBUG, INFO, WARN, or ERROR)", s)
	}
}
