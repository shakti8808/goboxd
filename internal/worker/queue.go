package worker

// queue is a bounded FIFO over Job values backed by a buffered channel.
//
// The architecture-spec Worker_Pool requires both bounded concurrency
// (Workers) and a bounded queue length (QueueLen) with FIFO order
// (Property 14) and immediate rejection when capacity is exhausted
// (Property 13). A buffered channel of capacity QueueLen gives all
// three for free: capacity is enforced by Go's runtime, ordering is
// enforced by channel semantics, and a non-blocking send (`select` with
// `default`) yields the immediate-rejection behaviour without a race
// window in which a freshly idle worker could accept a would-have-been-
// rejected submission.
//
// queue is intentionally minimal — Pool wraps it with the lifecycle
// (workers, drain, observer notifications). Keeping the data structure
// in its own file makes the property tests easier to reason about: the
// queue's invariants (FIFO + bounded) are independently testable.
type queue struct {
	c chan Job
}

// newQueue allocates a queue with the given capacity. capacity 0 is
// permitted: it produces a queue that accepts no buffered submissions
// and forces every Submit to land directly on a worker (or get
// rejected). The architecture spec permits 0 ≤ QueueLen ≤ 10000.
func newQueue(capacity int) *queue {
	return &queue{c: make(chan Job, capacity)}
}

// tryEnqueue attempts a non-blocking send onto the queue. It returns
// true if the job was buffered, false if the queue is full. The
// non-blocking semantics are what make Property 13 hold: a request
// arriving when both worker and queue capacity are exhausted gets a
// synchronous false here, never blocks waiting for a slot, and never
// races a freshly-idle worker.
func (q *queue) tryEnqueue(j Job) bool {
	select {
	case q.c <- j:
		return true
	default:
		return false
	}
}

// recv returns the worker-facing receive channel.
//
// Workers loop on `select { case j := <-q.recv(): … case <-stop: … }`;
// queue itself does not own the worker lifecycle.
func (q *queue) recv() <-chan Job { return q.c }

// drain returns every job buffered in the queue, in FIFO order, without
// blocking. Used by Pool.Drain when the drain timeout is 0 (or has
// elapsed) to deliver Outcome{Err: ctx.Canceled} to every queued
// submission's Result channel — discarded queued jobs MUST still
// receive a terminal outcome so the run handler does not hang.
func (q *queue) drain() []Job {
	out := make([]Job, 0, cap(q.c))
	for {
		select {
		case j := <-q.c:
			out = append(out, j)
		default:
			return out
		}
	}
}

// depth returns the current queue length. The Worker_Pool exposes this
// to the metrics collector and the architecture-spec gauge
// goboxd_worker_pool_queue_depth.
func (q *queue) depth() int { return len(q.c) }

