package runner_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/thesouldev/goboxd/internal/runner"
)

// TestCaptureUnderLimit confirms Capture retains every byte when the
// producer emits fewer bytes than the limit.
func TestCaptureUnderLimit(t *testing.T) {
	t.Parallel()
	res, err := runner.Capture(strings.NewReader("hello"), 1024)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if string(res.Retained) != "hello" {
		t.Errorf("Retained = %q, want %q", res.Retained, "hello")
	}
	if res.Total != 5 {
		t.Errorf("Total = %d, want 5", res.Total)
	}
	if res.Truncated {
		t.Error("Truncated = true; want false")
	}
}

// TestCaptureExactLimit asserts no truncation when produced bytes equal
// the cap exactly.
func TestCaptureExactLimit(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("X", 1024)
	res, err := runner.Capture(strings.NewReader(in), 1024)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Retained) != 1024 {
		t.Errorf("len(Retained) = %d, want 1024", len(res.Retained))
	}
	if res.Total != 1024 {
		t.Errorf("Total = %d, want 1024", res.Total)
	}
	if res.Truncated {
		t.Error("Truncated = true at exactly the cap; want false")
	}
}

// TestCaptureOverLimit asserts the Truncated flag fires the first byte
// past the cap and that Retained stops growing.
func TestCaptureOverLimit(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("Y", 1025)
	res, err := runner.Capture(strings.NewReader(in), 1024)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Retained) != 1024 {
		t.Errorf("len(Retained) = %d, want 1024 (cap)", len(res.Retained))
	}
	if res.Total != 1025 {
		t.Errorf("Total = %d, want 1025 (full producer count)", res.Total)
	}
	if !res.Truncated {
		t.Error("Truncated = false; want true")
	}
}

// TestCaptureOverLimit4x asserts the memory bound: even when the producer
// emits 4× the cap, the retained slice never grows beyond cap.
func TestCaptureOverLimit4x(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("Z", 4096)
	res, err := runner.Capture(strings.NewReader(in), 1024)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if cap(res.Retained) != 1024 {
		t.Errorf("cap(Retained) = %d, want 1024 (no slab growth)", cap(res.Retained))
	}
	if len(res.Retained) != 1024 {
		t.Errorf("len(Retained) = %d, want 1024", len(res.Retained))
	}
	if res.Total != 4096 {
		t.Errorf("Total = %d, want 4096", res.Total)
	}
	if !res.Truncated {
		t.Error("Truncated = false; want true")
	}
}

// TestCaptureZeroLimit confirms limit=0 discards everything but still
// drains the producer and reports Total/Truncated correctly.
func TestCaptureZeroLimit(t *testing.T) {
	t.Parallel()
	res, err := runner.Capture(strings.NewReader("data"), 0)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Retained) != 0 {
		t.Errorf("len(Retained) = %d, want 0", len(res.Retained))
	}
	if res.Total != 4 {
		t.Errorf("Total = %d, want 4", res.Total)
	}
	if !res.Truncated {
		t.Error("Truncated = false at limit=0 with non-empty producer; want true")
	}
}

// TestCaptureNegativeLimit asserts the input-validation guard.
func TestCaptureNegativeLimit(t *testing.T) {
	t.Parallel()
	_, err := runner.Capture(strings.NewReader("data"), -1)
	if !errors.Is(err, runner.ErrInvalidCaptureLimit) {
		t.Errorf("err = %v, want ErrInvalidCaptureLimit", err)
	}
}

// TestCaptureNilReader confirms the nil-reader branch.
func TestCaptureNilReader(t *testing.T) {
	t.Parallel()
	res, err := runner.Capture(nil, 1024)
	if err != nil {
		t.Fatalf("Capture(nil): %v", err)
	}
	if res.Total != 0 || res.Truncated || len(res.Retained) != 0 {
		t.Errorf("Capture(nil) = %+v, want zero-value", res)
	}
}

// TestCaptureProducerError surfaces a non-EOF reader error.
func TestCaptureProducerError(t *testing.T) {
	t.Parallel()
	r := iotest.ErrReader(errors.New("disk gone"))
	_, err := runner.Capture(r, 1024)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "disk gone") {
		t.Errorf("err = %v, want underlying message preserved", err)
	}
}

// TestCaptureChunkBoundary uses iotest.OneByteReader so the cap boundary
// falls inside a chunk and the truncation logic can't accidentally
// skip checking on every byte.
func TestCaptureChunkBoundary(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("a", 1100)
	r := iotest.OneByteReader(strings.NewReader(in))
	res, err := runner.Capture(r, 1024)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Retained) != 1024 {
		t.Errorf("len(Retained) = %d, want 1024", len(res.Retained))
	}
	if res.Total != 1100 {
		t.Errorf("Total = %d, want 1100", res.Total)
	}
	if !res.Truncated {
		t.Error("Truncated = false; want true")
	}
}

// slowReader emits bytes from src in chunks of size <= maxChunk so tests
// can exercise arbitrary read schedules.
type slowReader struct {
	src      *bytes.Reader
	maxChunk int
}

func (s *slowReader) Read(p []byte) (int, error) {
	if len(p) > s.maxChunk {
		p = p[:s.maxChunk]
	}
	return s.src.Read(p)
}

// TestCaptureRespectsChunkSize confirms the chunk buffer is at most
// CaptureChunkSize even when the producer would happily fill more.
func TestCaptureRespectsChunkSize(t *testing.T) {
	t.Parallel()
	if runner.CaptureChunkSize <= 0 {
		t.Fatalf("CaptureChunkSize = %d, want positive", runner.CaptureChunkSize)
	}
	in := strings.Repeat("Q", runner.CaptureChunkSize*3)
	res, err := runner.Capture(io.NopCloser(strings.NewReader(in)).(io.Reader), runner.CaptureChunkSize*2)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Retained) != runner.CaptureChunkSize*2 {
		t.Errorf("len(Retained) = %d, want %d", len(res.Retained), runner.CaptureChunkSize*2)
	}
	if !res.Truncated {
		t.Error("Truncated = false; want true")
	}
}
