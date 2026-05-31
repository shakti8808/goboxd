// Package worker owns the bounded FIFO worker pool that fronts the
// SandboxRunner. Architecture spec §10 (Worker_Pool Design) and the
// related architecture requirements REQ A-9.1..A-9.9 specify:
//
//   - At most Workers concurrent jobs.
//   - At most QueueLen queued jobs.
//   - FIFO dequeue order.
//   - Immediate rejection when both worker and queue capacity are
//     exhausted (Property 13).
//   - Drain semantics: T>0 waits up to T seconds for in-flight jobs to
//     complete, then cancels the rest; T=0 cancels in-flight
//     immediately and discards queued submissions.
//
// Wave D ships only the pool. The run handler in this same wave
// constructs Pool, the Wave E main wiring instantiates a single Pool
// at startup and threads it into the handler.
package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thesouldev/goboxd/internal/runner"
)

// Job is one work item the pool dispatches to a worker. The result of
// the per-job sandbox run is written to Result; callers MUST read from
// Result with a select that also honours their request context so a
// cancelled request does not block the worker forever.
type Job struct {
	// LanguageID is the registered language identifier; used as a
	// metric label and as a structured-log field.
	LanguageID string

	// Run is the runner.Job the SandboxRunner consumes.
	Run runner.Job

	// Stdin is the per-request stdin payload. Forwarded as the third
	// argument of SandboxRunner.Run.
	Stdin []byte

	// Ctx is the request-scoped context. The worker passes it to
	// SandboxRunner.Run; cancellation propagates into the sandbox
	// process tree.
	Ctx context.Context

	// Result receives the terminal outcome. Callers MUST allocate it
	// with capacity ≥ 1 so the worker's send never blocks even if the
	// request has already returned.
	Result chan<- Outcome
}

// Outcome is what the worker writes to Job.Result. Result + Err is the
// canonical Go pattern: Err is non-nil on hard errors (the runner could
// not produce a classified result at all); Result holds the runner's
// classified outcome for everything else.
type Outcome struct {
	Result runner.ExecutionResult
	Err    error
}

// JobRunner is the seam against runner.SandboxRunner. The interface
// exists so unit tests can substitute a stub; the production wire-up
// (Wave E) passes *runner.SandboxRunner directly because *SandboxRunner
// already satisfies the interface.
type JobRunner interface {
	Run(ctx context.Context, job runner.Job, stdin []byte) (runner.ExecutionResult, error)
}

// Observer is the runner-local hook the run handler in Wave E wires to
// internal/metrics. The pool reports queue-depth changes and drained
// rejections; tests inject a recording stub.
type Observer interface {
	OnQueueDepthChanged(depth int)
	OnSubmissionRejected(reason string)
}

// nopObserver is the default Observer.
type nopObserver struct{}

func (nopObserver) OnQueueDepthChanged(int)        {}
func (nopObserver) OnSubmissionRejected(string)    {}

// Config is the immutable configuration the Pool reads at construction.
type Config struct {
	// Workers is the maximum number of concurrent jobs. Architecture
	// spec REQ A-9.1 mandates 1 ≤ Workers ≤ 1024.
	Workers int

	// QueueLen is the maximum bounded queue length. Architecture spec
	// REQ A-9.2 mandates 0 ≤ QueueLen ≤ 10000.
	QueueLen int

	// DrainTimeout is the graceful-shutdown ceiling. 0 means
	// "cancel in-flight immediately" (REQ A-9.9); >0 up to 3600 means
	// "wait up to T seconds" (REQ A-9.7, A-9.8).
	DrainTimeout time.Duration

	// Runner is the JobRunner the workers dispatch to.
	Runner JobRunner

	// Observer is the runner-local observer surface; defaults to
	// nopObserver so tests and degraded-mode startups don't panic.
	Observer Observer
}

// Sentinel errors. The run handler maps ErrCapacityExhausted to HTTP
// 429 (REQ A-4.5) and ErrPoolStopped to HTTP 503 (graceful shutdown).
var (
	ErrInvalidWorkerConfig = errors.New("worker: invalid pool config")
	ErrCapacityExhausted   = errors.New("worker: capacity exhausted")
	ErrPoolStopped         = errors.New("worker: pool stopped")
)

// Documented architecture-spec ranges.
const (
	minWorkers      = 1
	maxWorkers      = 1024
	minQueueLen     = 0
	maxQueueLen     = 10000
	minDrainTimeout = 0
	maxDrainTimeout = 3600 * time.Second
)

// Pool is the bounded FIFO worker pool. Construct via New, drain via
// Drain. Pool is safe for concurrent use by multiple goroutines; the
// HTTP run handler shares one Pool across every request.
type Pool struct {
	cfg Config

	q          *queue
	stop       chan struct{}
	stopped    atomic.Bool
	wg         sync.WaitGroup
	stopOnce   sync.Once

	// activeCancel records the cancel function for every in-flight
	// job's worker context. Drain T=0 calls each one to abort the
	// runner; Drain T>0 waits and only calls them on timeout.
	activeMu     sync.Mutex
	activeCancel map[uint64]context.CancelFunc
	nextID       atomic.Uint64
}

// New constructs a Pool from cfg, validates ranges, applies defaults
// for optional fields, and starts the worker goroutines. The returned
// Pool is ready to receive Submit calls immediately.
//
// Callers MUST call Drain before discarding the *Pool; otherwise the
// worker goroutines leak.
func New(cfg Config) (*Pool, error) {
	if cfg.Workers < minWorkers || cfg.Workers > maxWorkers {
		return nil, fmt.Errorf("%w: Workers=%d outside [%d, %d]", ErrInvalidWorkerConfig, cfg.Workers, minWorkers, maxWorkers)
	}
	if cfg.QueueLen < minQueueLen || cfg.QueueLen > maxQueueLen {
		return nil, fmt.Errorf("%w: QueueLen=%d outside [%d, %d]", ErrInvalidWorkerConfig, cfg.QueueLen, minQueueLen, maxQueueLen)
	}
	if cfg.DrainTimeout < minDrainTimeout || cfg.DrainTimeout > maxDrainTimeout {
		return nil, fmt.Errorf("%w: DrainTimeout=%s outside [0, 3600s]", ErrInvalidWorkerConfig, cfg.DrainTimeout)
	}
	if cfg.Runner == nil {
		return nil, fmt.Errorf("%w: Runner is nil", ErrInvalidWorkerConfig)
	}
	if cfg.Observer == nil {
		cfg.Observer = nopObserver{}
	}

	p := &Pool{
		cfg:          cfg,
		q:            newQueue(cfg.QueueLen),
		stop:         make(chan struct{}),
		activeCancel: map[uint64]context.CancelFunc{},
	}
	for i := 0; i < cfg.Workers; i++ {
		p.wg.Add(1)
		go p.workerLoop()
	}
	return p, nil
}

// Submit hands job to the pool synchronously. It returns nil on accept
// (the worker will eventually write to job.Result), ErrCapacityExhausted
// when both worker and queue capacity are full, or ErrPoolStopped if
// Drain has begun.
//
// Submit never blocks: the rejection path is the architecture's
// "immediate" promise (Property 13). Callers MUST allocate job.Result
// with capacity ≥ 1.
func (p *Pool) Submit(ctx context.Context, job Job) error {
	if p.stopped.Load() {
		p.cfg.Observer.OnSubmissionRejected("pool_stopped")
		return ErrPoolStopped
	}
	// Defensive: a zero-length channel would deadlock the worker.
	if job.Result == nil || cap(job.Result) < 1 {
		return fmt.Errorf("%w: Job.Result must be a buffered channel with capacity ≥ 1", ErrInvalidWorkerConfig)
	}
	if job.Ctx == nil {
		job.Ctx = ctx
	}
	if !p.q.tryEnqueue(job) {
		p.cfg.Observer.OnSubmissionRejected("capacity_exhausted")
		return ErrCapacityExhausted
	}
	p.cfg.Observer.OnQueueDepthChanged(p.q.depth())
	return nil
}

// QueueDepth returns the current queue length. Used by the run handler
// for log entries and by tests for property assertions.
func (p *Pool) QueueDepth() int { return p.q.depth() }

// Drain stops the pool. The architecture-spec semantics:
//
//   - timeout > 0: stop accepting new submissions; wait up to timeout
//     for in-flight jobs to complete; on timeout cancel the remaining
//     in-flight contexts and drain queued jobs with Outcome{Err: ErrPoolStopped}.
//   - timeout == 0: stop accepting new submissions; cancel every
//     in-flight job immediately; drain queued jobs.
//
// Drain returns (drained, cancelled) where:
//   - drained is the count of in-flight jobs that completed before the
//     timeout (or 0 when timeout == 0).
//   - cancelled is the count of jobs cancelled (in-flight + queued).
//
// Drain may be called once. Subsequent calls return (0, 0) immediately.
func (p *Pool) Drain(timeout time.Duration) (drained int, cancelled int) {
	stopped := false
	p.stopOnce.Do(func() {
		p.stopped.Store(true)
		close(p.stop)
		stopped = true
	})
	if !stopped {
		return 0, 0
	}

	if timeout > 0 {
		// Snapshot the in-flight count before waiting; the wait may be
		// shorter than `timeout` if every job finishes early.
		done := make(chan struct{})
		go func() {
			p.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			drained = p.activeCount()
		case <-time.After(timeout):
			cancelled = p.cancelAll()
			p.wg.Wait()
			drained = 0
		}
	} else {
		cancelled = p.cancelAll()
		p.wg.Wait()
	}

	// Whatever survives in the queue did not run. Send a terminal
	// outcome to each so the originating run handler unblocks.
	queued := p.q.drain()
	for _, j := range queued {
		select {
		case j.Result <- Outcome{Err: ErrPoolStopped}:
		default:
		}
		cancelled++
	}
	p.cfg.Observer.OnQueueDepthChanged(p.q.depth())
	return drained, cancelled
}

// workerLoop is the per-worker dispatch loop. The architecture spec's
// FIFO + bounded-capacity invariants are enforced by the queue; this
// loop only adds the lifecycle and the per-job runner invocation.
func (p *Pool) workerLoop() {
	defer p.wg.Done()
	for {
		select {
		case <-p.stop:
			return
		case job := <-p.q.recv():
			p.cfg.Observer.OnQueueDepthChanged(p.q.depth())
			p.dispatch(job)
		}
	}
}

// dispatch runs one job. It threads a cancellable child context off
// job.Ctx so Drain T=0 (or Drain T>0's timeout branch) can cancel the
// underlying SandboxRunner.Run. The Outcome is delivered to job.Result
// before the worker becomes available again.
//
// Race-safety with Drain: Drain takes a snapshot of activeCancel and
// cancels every entry. If a worker registers AFTER the snapshot, the
// cancel is missed. To close the window, dispatch checks p.stopped
// AFTER registering its cancel; if stop has fired, dispatch cancels
// its own ctx so Run's ctx.Done() unblocks.
func (p *Pool) dispatch(job Job) {
	id := p.nextID.Add(1)
	ctx, cancel := context.WithCancel(job.Ctx)
	p.activeMu.Lock()
	p.activeCancel[id] = cancel
	p.activeMu.Unlock()

	// Race-safety post-registration: if Drain has fired between Submit
	// and now, self-cancel so Run's ctx.Done unblocks immediately.
	if p.stopped.Load() {
		cancel()
	}

	res, err := p.cfg.Runner.Run(ctx, job.Run, job.Stdin)

	p.activeMu.Lock()
	delete(p.activeCancel, id)
	p.activeMu.Unlock()
	cancel()

	out := Outcome{Result: res, Err: err}
	select {
	case job.Result <- out:
	default:
		// Result is buffered cap=1; this default arm only fires on a
		// programming bug in the caller. Drop the result quietly.
	}
}

// activeCount snapshots the in-flight job count.
func (p *Pool) activeCount() int {
	p.activeMu.Lock()
	defer p.activeMu.Unlock()
	return len(p.activeCancel)
}

// cancelAll cancels every in-flight job's child context. Returns the
// number of contexts cancelled.
func (p *Pool) cancelAll() int {
	p.activeMu.Lock()
	defer p.activeMu.Unlock()
	for _, cancel := range p.activeCancel {
		cancel()
	}
	n := len(p.activeCancel)
	p.activeCancel = map[uint64]context.CancelFunc{}
	return n
}
