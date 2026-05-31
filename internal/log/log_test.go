package log_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	goboxlog "github.com/thesouldev/goboxd/internal/log"
)

// iso8601msRE matches the ISO 8601 UTC millisecond format required by
// architecture REQ A-14.1 and Property 26.
var iso8601msRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

// TestFormatTime asserts the timestamp helper round-trips through the
// regexp expected by Property 26.
func TestFormatTime(t *testing.T) {
	t.Parallel()
	cases := []time.Time{
		time.Date(2025, 1, 2, 3, 4, 5, 6e6, time.UTC),                  // simple
		time.Date(2025, 1, 2, 3, 4, 5, 6e6, time.FixedZone("X", 7200)), // non-UTC input
		time.Now().UTC(),
	}
	for _, tc := range cases {
		got := goboxlog.FormatTime(tc)
		if !iso8601msRE.MatchString(got) {
			t.Errorf("FormatTime(%s) = %q, does not match %s", tc, got, iso8601msRE)
		}
	}
}

// TestParseLevel covers the closed accepted set plus the empty-string
// default and a rejection case.
func TestParseLevel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"DEBUG", slog.LevelDebug, false},
		{"info", slog.LevelInfo, false},
		{" warn ", slog.LevelWarn, false},
		{"WARNING", slog.LevelWarn, false},
		{"ERROR", slog.LevelError, false},
		{"", slog.LevelInfo, false},
		{"verbose", slog.LevelInfo, true},
	}
	for _, tc := range cases {
		got, err := goboxlog.ParseLevel(tc.in)
		if got != tc.want {
			t.Errorf("ParseLevel(%q) level = %v, want %v", tc.in, got, tc.want)
		}
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseLevel(%q) err = %v, wantErr=%v", tc.in, err, tc.wantErr)
		}
	}
}

// TestStructuredEntryShape decodes a single emitted log entry and asserts
// it carries the keys required by REQ A-14.1 (request_id, level, ts, msg)
// and that ts matches the ISO 8601 UTC ms regex.
func TestStructuredEntryShape(t *testing.T) {
	var buf bytes.Buffer
	logger := goboxlog.New(&buf, slog.LevelInfo)
	logger.Info("event_x",
		"event", "event_x",
		"request_id", "00000000-0000-4000-8000-000000000000",
	)

	if buf.Len() == 0 {
		t.Fatal("expected one log entry, got nothing")
	}
	line := strings.TrimSpace(buf.String())
	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("entry is not valid JSON: %v\nline=%s", err, line)
	}

	for _, k := range []string{"ts", "level", "msg", "event", "request_id"} {
		if _, ok := entry[k]; !ok {
			t.Errorf("entry missing required key %q: %v", k, entry)
		}
	}
	ts, _ := entry["ts"].(string)
	if !iso8601msRE.MatchString(ts) {
		t.Errorf("ts = %q does not match %s", ts, iso8601msRE)
	}
	if entry["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", entry["level"])
	}
}

// TestNilWriterFallsBackToStderr checks New does not panic when w is nil.
func TestNilWriterFallsBackToStderr(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("New(nil, ...) panicked: %v", r)
		}
	}()
	logger := goboxlog.New(nil, slog.LevelInfo)
	if logger == nil {
		t.Fatal("New returned nil")
	}
}
