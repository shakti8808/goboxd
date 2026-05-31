package runner_test

import (
	"bytes"
	"testing"

	"pgregory.net/rapid"

	"github.com/thesouldev/goboxd/internal/runner"
)

// TestPropertyCaptureBoundedAndTruncationFlag — Property 38.
//
// For arbitrary (limit, producedBytes) pairs and arbitrary read schedules:
//
//   - len(Retained) == min(producedBytes, limit)
//   - Total == producedBytes (every emitted byte is counted)
//   - Truncated iff producedBytes > limit
//   - cap(Retained) <= limit (memory bound — slab never grows past cap)
//
// The chunk buffer is bounded by CaptureChunkSize by construction; the
// property test focuses on the retained-slab side of the memory bound,
// which is the one a hostile producer can attack.
func TestPropertyCaptureBoundedAndTruncationFlag(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Cap the limit at 4 KiB so each iteration is cheap; the
		// architecture spec's range is 1..10 MiB but the property is
		// invariant over the cap value.
		limit := rapid.IntRange(0, 4096).Draw(t, "limit")

		// Produce up to 4× the cap so we exercise both the under-cap
		// and over-cap branches with reasonable density.
		produced := rapid.IntRange(0, limit*4+limit+1).Draw(t, "produced")

		// Build the producer payload and a slow reader so chunk
		// arrival order varies.
		payload := make([]byte, produced)
		for i := range payload {
			payload[i] = byte('a' + i%26)
		}
		maxChunk := rapid.IntRange(1, 1024).Draw(t, "maxChunk")
		r := &slowReader{src: bytes.NewReader(payload), maxChunk: maxChunk}

		res, err := runner.Capture(r, limit)
		if err != nil {
			t.Fatalf("Capture: %v", err)
		}

		wantRetainedLen := produced
		if wantRetainedLen > limit {
			wantRetainedLen = limit
		}
		if len(res.Retained) != wantRetainedLen {
			t.Fatalf("len(Retained) = %d, want %d (limit=%d produced=%d)",
				len(res.Retained), wantRetainedLen, limit, produced)
		}
		if cap(res.Retained) > limit {
			t.Fatalf("cap(Retained) = %d, exceeds limit %d (memory bound violated)",
				cap(res.Retained), limit)
		}
		if int(res.Total) != produced {
			t.Fatalf("Total = %d, want %d", res.Total, produced)
		}
		if got, want := res.Truncated, produced > limit; got != want {
			t.Fatalf("Truncated = %v, want %v (limit=%d produced=%d)",
				got, want, limit, produced)
		}
	})
}

// TestPropertyCapturePrefixIsByteEqual — Property 38 corollary.
//
// Whatever Capture retains MUST be the prefix of the producer's bytes —
// truncation discards the *suffix*, never the head. This is what makes
// the captured output safe to surface to clients: log lines aren't
// scrambled, error tracebacks aren't sliced from the middle, etc.
func TestPropertyCapturePrefixIsByteEqual(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		limit := rapid.IntRange(0, 4096).Draw(t, "limit")
		produced := rapid.IntRange(0, limit*2+1).Draw(t, "produced")

		payload := make([]byte, produced)
		for i := range payload {
			payload[i] = byte(i % 256)
		}
		res, err := runner.Capture(bytes.NewReader(payload), limit)
		if err != nil {
			t.Fatalf("Capture: %v", err)
		}

		expected := payload
		if len(expected) > limit {
			expected = expected[:limit]
		}
		if !bytes.Equal(res.Retained, expected) {
			t.Fatalf("Retained mismatch: got %v want %v", res.Retained, expected)
		}
	})
}
