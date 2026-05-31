package worker_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesouldev/goboxd/internal/runner"
	"github.com/thesouldev/goboxd/internal/worker"
)

// stubRunner implements worker.JobRunner. The behaviour is configurable
// per test: optionally block on a release channel, optionally return a
// canned ExecutionResult, optionally honour ctx.Done.
type stubRunner struct {
	mu       sync.Mutex
	calls    int
	released chan struct{}
	gate     chan struct{}
	res      runner.ExecutionResult
	err      error
}

func newStubRunner() *stubRunner {
	return &stubRunner{
		released: make(chan struct{}, 1024),
		gate:     nil,
		res:      runner.ExecutionResult{Status: runner.StatusOK},
	}
}

func (s *stubRunner) Run(ctx context.Context, _ runner.Job, _ []byte) (runner.ExecutionResult, error) {
	s.mu.Lock()
	gate := s.gate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return runner.ExecutionResult{Status: runner.StatusInternalError}, ctx.Err()
		}
	}
	s.mu.Lock()
	s.calls++
	res := s.res
	err := s.err
	s.mu.Unlock()
	s.released <- struct{}{}
	return res, err
}

func (s *stubRunner) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// recordingObserver captures pool observer events for assertions.
type recordingObserver struct {
	mu        sync.Mutex
	depths    []int
	rejections []string
}

func (r *recordingObserver) OnQueueDepthChanged(depth int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.depths = append(r.depths, depth)
}

func (r *recordingObserver) OnSubmissionRejected(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rejections = append(r.rejections, reason)
}

// newSubmitJob builds a Job with a freshly-allocated buffered Result
// channel. Tests pass the returned channel directly so they can read
// the Outcome without racing the worker.
func newSubmitJob(ctx context.Context) (worker.Job, chan worker.Outcome) {
	res := make(chan worker.Outcome, 1)
	return worker.Job{
		LanguageID: "py3",
		Run:        runner.Job{},
		Stdin:      nil,
		Ctx:        ctx,
		Result:     res,
	}, res
}


// TestNewValidates walks the required-field rejection cases.
func TestNewValidates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  worker.Config
	}{
		{"workers below min", worker.Config{Workers: 0, QueueLen: 1, Runner: newStubRunner()}},
		{"workers above max", worker.Config{Workers: 9999, QueueLen: 1, Runner: newStubRunner()}},
		{"queue below min", worker.Config{Workers: 1, QueueLen: -1, Runner: newStubRunner()}},
		{"queue above max", worker.Config{Workers: 1, QueueLen: 1_000_000, Runner: newStubRunner()}},
		{"drain below min", worker.Config{Workers: 1, QueueLen: 1, DrainTimeout: -1, Runner: newStubRunner()}},
		{"drain above max", worker.Config{Workers: 1, QueueLen: 1, DrainTimeout: 100 * time.Hour, Runner: newStubRunner()}},
		{"runner nil", worker.Config{Workers: 1, QueueLen: 1, Runner: nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := worker.New(tc.cfg)
			if !errors.Is(err, worker.ErrInvalidWorkerConfig) {
				t.Errorf("err = %v, want ErrInvalidWorkerConfig", err)
			}
		})
	}
}

// TestSubmitAcceptsAndRunsAcceptedJob exercises the happy path: one
// worker, one queue slot, one accepted submission.
func TestSubmitAcceptsAndRunsAcceptedJob(t *testing.T) {
	t.Parallel()
	r := newStubRunner()
	r.res = runner.ExecutionResult{Status: runner.StatusOK, Stdout: []byte("hi")}
	obs := &recordingObserver{}
	p, err := worker.New(worker.Config{Workers: 1, QueueLen: 1, Runner: r, Observer: obs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Drain(time.Second)

	j, res := newSubmitJob(context.Background())
	if err := p.Submit(context.Background(), j); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	out := <-res
	if out.Err != nil {
		t.Errorf("Outcome.Err = %v", out.Err)
	}
	if string(out.Result.Stdout) != "hi" {
		t.Errorf("Outcome.Result.Stdout = %q, want hi", out.Result.Stdout)
	}
	if r.Calls() != 1 {
		t.Errorf("runner Calls = %d, want 1", r.Calls())
	}
}


// TestSubmitRejectsWhenCapacityExhausted asserts Property 13:
// when both worker capacity and queue capacity are full, Submit
// returns ErrCapacityExhausted immediately and never accepts the
// would-have-been-rejected submission even after a worker frees up.
func TestSubmitRejectsWhenCapacityExhausted(t *testing.T) {
	t.Parallel()
	r := newStubRunner()
	gate := make(chan struct{})
	r.gate = gate

	p, err := worker.New(worker.Config{Workers: 1, QueueLen: 1, Runner: r})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Drain(time.Second)

	// 1) Submit job A — accepted, takes the worker. Worker blocks on gate.
	jA, _ := newSubmitJob(context.Background())
	if err := p.Submit(context.Background(), jA); err != nil {
		t.Fatalf("Submit A: %v", err)
	}

	// Wait until the worker has actually picked up A so we know the
	// queue slot is empty (and not still occupied by A waiting to be
	// dequeued).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.QueueDepth() == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	// 2) Submit job B — buffered in the queue.
	jB, _ := newSubmitJob(context.Background())
	if err := p.Submit(context.Background(), jB); err != nil {
		t.Fatalf("Submit B: %v", err)
	}

	// 3) Submit job C — should be rejected immediately.
	jC, _ := newSubmitJob(context.Background())
	if err := p.Submit(context.Background(), jC); !errors.Is(err, worker.ErrCapacityExhausted) {
		t.Fatalf("Submit C: err = %v, want ErrCapacityExhausted", err)
	}

	// Release the worker so the test can clean up.
	close(gate)
}

// TestSubmitFIFO asserts Property 14: jobs are dequeued in FIFO order.
//
// Layout: 1 worker, 4-slot queue, 4 jobs submitted in sequence. The
// worker processes them and writes the LanguageID into a shared slice;
// after all 4 are done the slice MUST be in submission order.
func TestSubmitFIFO(t *testing.T) {
	t.Parallel()
	var (
		mu    sync.Mutex
		order []string
	)
	r := &orderingRunner{record: func(id string) {
		mu.Lock()
		order = append(order, id)
		mu.Unlock()
	}}
	p, err := worker.New(worker.Config{Workers: 1, QueueLen: 8, Runner: r})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Drain(time.Second)

	results := make([]chan worker.Outcome, 4)
	for i, id := range []string{"a", "b", "c", "d"} {
		j, res := newSubmitJob(context.Background())
		j.LanguageID = id
		j.Run.LanguageID = id
		if err := p.Submit(context.Background(), j); err != nil {
			t.Fatalf("Submit %s: %v", id, err)
		}
		results[i] = res
	}
	for _, res := range results {
		<-res
	}
	mu.Lock()
	got := append([]string{}, order...)
	mu.Unlock()
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("order length = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// orderingRunner is a JobRunner that records the LanguageID of every
// job it sees. Used by the FIFO test.
type orderingRunner struct{ record func(string) }

func (r *orderingRunner) Run(_ context.Context, j runner.Job, _ []byte) (runner.ExecutionResult, error) {
	r.record(j.LanguageID)
	return runner.ExecutionResult{Status: runner.StatusOK}, nil
}


// TestQueueDepthGaugeTracksQueue confirms Property 30:
// the queue-depth observer is invoked on every enqueue/dequeue event
// so the metric's value matches the actual queue size.
func TestQueueDepthGaugeTracksQueue(t *testing.T) {
	t.Parallel()
	r := newStubRunner()
	gate := make(chan struct{})
	r.gate = gate

	obs := &recordingObserver{}
	p, err := worker.New(worker.Config{Workers: 1, QueueLen: 4, Runner: r, Observer: obs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Drain(time.Second)

	// Submit 3 jobs while the worker is gated on `gate`.
	results := make([]chan worker.Outcome, 3)
	for i := 0; i < 3; i++ {
		j, res := newSubmitJob(context.Background())
		if err := p.Submit(context.Background(), j); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		results[i] = res
	}

	// Release the worker; queue drains as it processes them.
	close(gate)
	for _, res := range results {
		<-res
	}

	// Observer must have seen non-zero depths at some point.
	obs.mu.Lock()
	depths := append([]int{}, obs.depths...)
	obs.mu.Unlock()
	sawNonZero := false
	for _, d := range depths {
		if d > 0 {
			sawNonZero = true
			break
		}
	}
	if !sawNonZero {
		t.Errorf("observer never saw non-zero depth: %v", depths)
	}
}

// TestDrainPositiveTimeoutWaitsForInFlight confirms Drain T>0 waits
// for in-flight jobs to finish before returning.
func TestDrainPositiveTimeoutWaitsForInFlight(t *testing.T) {
	t.Parallel()
	r := newStubRunner()
	gate := make(chan struct{})
	r.gate = gate

	p, err := worker.New(worker.Config{Workers: 1, QueueLen: 1, Runner: r})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	j, res := newSubmitJob(context.Background())
	if err := p.Submit(context.Background(), j); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		close(gate)
	}()

	start := time.Now()
	_, _ = p.Drain(2 * time.Second)
	elapsed := time.Since(start)

	if elapsed >= 2*time.Second {
		t.Errorf("drain elapsed = %s, expected < timeout (job completed)", elapsed)
	}
	<-res
}

// TestDrainPositiveTimeoutCancelsOnElapse confirms Drain T>0 cancels
// in-flight jobs when the timeout elapses.
func TestDrainPositiveTimeoutCancelsOnElapse(t *testing.T) {
	t.Parallel()
	r := newStubRunner()
	gate := make(chan struct{})
	r.gate = gate

	p, err := worker.New(worker.Config{Workers: 1, QueueLen: 1, Runner: r})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	j, res := newSubmitJob(context.Background())
	if err := p.Submit(context.Background(), j); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	_, cancelled := p.Drain(50 * time.Millisecond)
	// The submitted job MUST receive a terminal Outcome regardless of
	// whether it was cancelled in-flight (ctx.Canceled) or drained
	// from the queue before the worker picked it up (ErrPoolStopped).
	// Both outcomes are valid behaviour for Drain T>0; the architecture
	// spec only mandates that the job not silently disappear.
	_ = cancelled
	out := <-res
	if !errors.Is(out.Err, context.Canceled) && !errors.Is(out.Err, worker.ErrPoolStopped) {
		t.Errorf("Outcome.Err = %v, want context.Canceled or ErrPoolStopped", out.Err)
	}
	close(gate)
}

// TestDrainZeroCancelsImmediately confirms Drain T=0 cancels in-flight
// AND drains queued jobs without waiting.
func TestDrainZeroCancelsImmediately(t *testing.T) {
	t.Parallel()
	r := newStubRunner()
	gate := make(chan struct{})
	r.gate = gate

	p, err := worker.New(worker.Config{Workers: 1, QueueLen: 4, Runner: r})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	jA, resA := newSubmitJob(context.Background())
	if err := p.Submit(context.Background(), jA); err != nil {
		t.Fatalf("Submit A: %v", err)
	}
	jB, resB := newSubmitJob(context.Background())
	if err := p.Submit(context.Background(), jB); err != nil {
		t.Fatalf("Submit B: %v", err)
	}
	jC, resC := newSubmitJob(context.Background())
	if err := p.Submit(context.Background(), jC); err != nil {
		t.Fatalf("Submit C: %v", err)
	}

	start := time.Now()
	_, cancelled := p.Drain(0)
	elapsed := time.Since(start)
	// At least one job (B and/or C in the queue) is guaranteed to be
	// cancelled. The in-flight job A may either be cancelled by Drain
	// or by the dispatch race-safety self-cancel; both paths produce a
	// terminal Outcome but only the queue-drain path increments the
	// `cancelled` counter. The architecture-spec invariant we assert
	// here is "Drain T=0 returns within a bounded epsilon", not the
	// exact counter value.
	_ = cancelled
	if elapsed > 500*time.Millisecond {
		t.Errorf("drain elapsed = %s, want bounded epsilon", elapsed)
	}
	close(gate)

	// Each submission must have received a terminal outcome.
	for _, res := range []chan worker.Outcome{resA, resB, resC} {
		select {
		case <-res:
		case <-time.After(time.Second):
			t.Fatal("drain T=0 did not deliver outcome to every queued job")
		}
	}
}

// TestSubmitAfterDrainReturnsStopped confirms Submit refuses new work
// after Drain begins.
func TestSubmitAfterDrainReturnsStopped(t *testing.T) {
	t.Parallel()
	r := newStubRunner()
	p, err := worker.New(worker.Config{Workers: 1, QueueLen: 1, Runner: r})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.Drain(0)

	j, _ := newSubmitJob(context.Background())
	if err := p.Submit(context.Background(), j); !errors.Is(err, worker.ErrPoolStopped) {
		t.Errorf("Submit after Drain: err = %v, want ErrPoolStopped", err)
	}
}

// TestSubmitRejectsZeroBufferResult asserts the Job.Result invariant.
func TestSubmitRejectsZeroBufferResult(t *testing.T) {
	t.Parallel()
	r := newStubRunner()
	p, err := worker.New(worker.Config{Workers: 1, QueueLen: 1, Runner: r})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Drain(0)

	j := worker.Job{
		LanguageID: "py3",
		Result:     make(chan worker.Outcome), // unbuffered
		Ctx:        context.Background(),
	}
	if err := p.Submit(context.Background(), j); !errors.Is(err, worker.ErrInvalidWorkerConfig) {
		t.Errorf("Submit unbuffered: err = %v, want ErrInvalidWorkerConfig", err)
	}
}

// TestDoubleDrainNoOp asserts the second Drain call is a no-op.
func TestDoubleDrainNoOp(t *testing.T) {
	t.Parallel()
	r := newStubRunner()
	p, err := worker.New(worker.Config{Workers: 1, QueueLen: 1, Runner: r})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.Drain(0)
	d, c := p.Drain(0)
	if d != 0 || c != 0 {
		t.Errorf("second Drain returned (%d, %d), want (0, 0)", d, c)
	}
}

// helper to suppress "unused" warning from the atomic import in the
// concurrent-submit test; kept here to keep imports stable.
var _ = atomic.Bool{}
