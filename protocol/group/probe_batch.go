package group

import (
	"context"
	"sync"
)

// probeBatch runs one health round's measurements on a fixed set of workers.
//
// # Why not batch.Batch
//
// The generic batch helper starts ONE GOROUTINE PER TASK and only then waits for a concurrency
// token. For a large provider that is the wrong shape: a 10,000-node round created 10,000 goroutines
// that were merely parked on the token - measured at ~9,650 goroutines for a 10,000-member round -
// before a single probe ran. Each parked goroutine holds a stack, and on a NetworkExtension with a
// 50 MiB budget that is memory spent on waiting rather than on measuring.
//
// This type keeps the same contract the round needs - submit by key, bounded per-round concurrency,
// join on Wait, cancel the round on the first task error - but the bound is applied BEFORE the
// task is accepted: a full queue blocks the traversal, and only the workers' goroutines exist.
//
// The round's per-member panic boundary stays where it was (urlTestBatch.measure); a task panic
// still takes the worker down exactly as it took the batch goroutine down before, so this type
// does not change panic semantics.
type probeBatch struct {
	tasks  chan probeTask
	cancel context.CancelFunc

	access sync.Mutex
	err    error

	waitOnce sync.Once
	workers  sync.WaitGroup
}

type probeTask struct {
	key string
	fn  func() (any, error)
}

// newProbeBatch starts workers measurement goroutines. queueSize bounds how many submitted tasks may
// wait ahead of the workers; a non-positive size is treated as one per worker.
func newProbeBatch(cancel context.CancelFunc, workers int, queueSize int) *probeBatch {
	if workers <= 0 {
		workers = 1
	}
	if queueSize <= 0 {
		queueSize = workers
	}
	batch := &probeBatch{
		tasks:  make(chan probeTask, queueSize),
		cancel: cancel,
	}
	batch.workers.Add(workers)
	for range workers {
		go batch.worker()
	}
	return batch
}

func (b *probeBatch) worker() {
	defer b.workers.Done()
	for task := range b.tasks {
		_, err := task.fn()
		if err != nil {
			b.access.Lock()
			if b.err == nil {
				b.err = err
				if b.cancel != nil {
					// Same contract as the generic batch: the first task error aborts the round.
					b.cancel()
				}
			}
			b.access.Unlock()
		}
	}
}

// Go submits one task. It blocks while the queue is full, which is what keeps the caller's
// traversal from producing more work than the round can hold; the round context bounds the work
// itself once it starts.
func (b *probeBatch) Go(key string, fn func() (any, error)) {
	b.tasks <- probeTask{key: key, fn: fn}
}

// Wait joins the workers and returns the first task error, if any. It is safe to call more than
// once: the round calls it both on the normal path and from its panic-safe defer.
func (b *probeBatch) Wait() error {
	b.waitOnce.Do(func() {
		close(b.tasks)
		b.workers.Wait()
	})
	return b.err
}
