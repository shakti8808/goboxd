package runner

import (
	"errors"
	"fmt"
	"io"
)

// CaptureChunkSize is the maximum bytes Capture pulls from the underlying
// io.Reader in a single Read call. Architecture §11 "Streaming Output
// Protection" requires chunked reads ≤16 KiB so a hostile producer cannot
// allocate a single oversize buffer inside the runner.
const CaptureChunkSize = 16 * 1024

// CaptureResult carries the bounded retention of a streaming capture.
//
// Retained holds the first min(Total, limit) bytes of the underlying
// reader. Total is the unbounded count of bytes the producer emitted;
// Truncated reports whether Total exceeded the configured limit.
type CaptureResult struct {
	// Retained is the bytes kept for the Execution_Result envelope.
	// Length is at most the limit passed to Capture; reading past the
	// retained slice returns no further data even if the producer
	// continued emitting bytes.
	Retained []byte

	// Total is the wall-clock byte count emitted by the producer
	// (including bytes that were discarded after the cap was reached).
	// Useful for the truncation flag and for metrics reporting.
	Total int64

	// Truncated reports whether Total exceeded the configured limit.
	// Architecture §11 ties this flag directly to the
	// Execution_Result.{stdout,stderr}_truncated booleans.
	Truncated bool
}

// ErrInvalidCaptureLimit is returned by Capture when the limit argument
// is negative.
var ErrInvalidCaptureLimit = errors.New("runner: capture limit must be non-negative")

// Capture reads bytes from r into a memory-bounded buffer.
//
// While total bytes read < limit, Capture appends incoming bytes to the
// retained slab. Once the cumulative byte count reaches limit, Capture
// stops growing the slab and discards subsequent bytes streamingly: it
// continues to call Read on r so the producer can run to completion,
// but the chunk buffer is reused and the retained slab does not grow.
// Memory used by Capture is bounded by:
//
//	limit + CaptureChunkSize + O(1)
//
// regardless of how many bytes r emits (Property 38).
//
// limit == 0 discards every byte the producer emits; the Truncated flag
// fires when even one byte arrives. limit < 0 returns
// ErrInvalidCaptureLimit. r == nil returns the zero-value CaptureResult.
//
// Capture treats io.EOF and io.ErrUnexpectedEOF as success terminators.
// Any other read error is wrapped and returned alongside whatever bytes
// have been retained up to that point (so callers can still surface
// partial output to the caller for debugging when appropriate).
func Capture(r io.Reader, limit int) (CaptureResult, error) {
	if limit < 0 {
		return CaptureResult{}, ErrInvalidCaptureLimit
	}
	if r == nil {
		return CaptureResult{Retained: []byte{}}, nil
	}

	// Pre-allocate the retained slab to its capacity ceiling so growth
	// never reallocates and the memory bound is observable. limit == 0
	// produces a zero-length slab.
	retained := make([]byte, 0, limit)

	chunk := make([]byte, CaptureChunkSize)
	var total int64
	var truncated bool
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			total += int64(n)
			room := limit - len(retained)
			if room > 0 {
				take := n
				if take > room {
					take = room
				}
				retained = append(retained, chunk[:take]...)
			}
			if total > int64(limit) {
				truncated = true
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return CaptureResult{Retained: retained, Total: total, Truncated: truncated}, nil
			}
			return CaptureResult{Retained: retained, Total: total, Truncated: truncated},
				fmt.Errorf("runner: capture: %w", err)
		}
		if n == 0 {
			// A zero-byte read with no error is a misbehaved Reader.
			// Treat it as EOF so we don't spin; the io contract permits
			// this only when limit-bytes-read have been signalled via
			// EOF on the next call, but defensively we exit here.
			return CaptureResult{Retained: retained, Total: total, Truncated: truncated}, nil
		}
	}
}
