package worker_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/thesouldev/goboxd/internal/runner"
	"github.com/thesouldev/goboxd/internal/worker"
)


// TestPropertyCapacityBoundsHold — Property 12.
//
// For arbitrary submission rates and worker-completion timings, the
// pool's queue depth never exceeds QueueLen. The runner blocks on a
// gate so submissions stack up in the queue; the observer records the
// peak depth.
//
// Rapid runs ≥100 iterations per invocation per Req I-4.2.
func TestPropertyCapacityBoundsHold(outer *testing.T) {
	rapid.Check(outer, func(t *rapid.T) {
		workers := rapid.IntRange(1, 4).Draw(t, "workers")
		queueLen := rapid.IntRange(0, 4).Draw(t, "queueLen")
		submissions := rapid.IntRange(0, 16).Draw(t, "submissions")

		gate := make(chan struct{})
		r := &boundsRunner{gate: gate}
		obs := &boundsObserver{}

		p, err := worker.New(worker.Config{
			Workers:  workers,
			QueueLen: queueLen,
			Runner:   r,
			Observer: obs,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		results := make([]chan worker.Outcome, 0, submissions)
		for i := 0; i < submissions; i++ {
			j, res := newSubmitJob(context.Background())
			if err := p.Submit(context.Background(), j); err == nil {
				results = append(results, res)
			}
		}

		obs.mu.Lock()
		maxDepth := obs.maxDepth
		obs.mu.Unlock()
		if maxDepth > queueLen {
			t.Fatalf("queueDepth peaked at %d, exceeds QueueLen=%d", maxDepth, queueLen)
		}

		close(gate)
		for _, res := range results {
			select {
			case <-res:
			case <-time.After(2 * time.Second):
				t.Fatal("worker never delivered outcome")
			}
		}
		p.Drain(time.Second)
	})
}

// boundsRunner blocks on gate so we can observe the queue at full
// capacity.
type boundsRunner struct{ gate chan struct{} }

func (b *boundsRunner) Run(ctx context.Context, _ runner.Job, _ []byte) (runner.ExecutionResult, error) {
	select {
	case <-b.gate:
	case <-ctx.Done():
		return runner.ExecutionResult{Status: runner.StatusInternalError}, ctx.Err()
	}
	return runner.ExecutionResult{Status: runner.StatusOK}, nil
}

// boundsObserver records the maximum queue depth observed.
type boundsObserver struct {
	mu       sync.Mutex
	maxDepth int
}

func (o *boundsObserver) OnQueueDepthChanged(d int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if d > o.maxDepth {
		o.maxDepth = d
	}
}

func (o *boundsObserver) OnSubmissionRejected(string) {}
