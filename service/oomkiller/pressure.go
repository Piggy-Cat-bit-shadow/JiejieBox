package oomkiller

// notifyPressure handles a memory-pressure event from the platform's dispatch source.
//
// # Why this lives outside the darwin-only file
//
// The logic is not Darwin-specific: it is the callback lifecycle the one Darwin dispatch source in
// this tree drives. Keeping it behind `darwin && cgo` made it testable only on a machine that could
// build the C half, which is exactly where the lifecycle races are hardest to reproduce. The
// platform-neutral file compiles and is exercised everywhere; service_darwin.go remains the only
// caller, so no non-Darwin build acquires a new entry point.
//
// # The two-part guarantee
//
// A callback must not do destructive work on a stopped timer, AND Close must not return while a
// callback that legitimately started can still write MemoryPressureCritical. Those are different
// requirements and the first does not imply the second:
//
//	active check    -> bounds WHEN the work may start
//	in-flight group -> bounds when it has FINISHED, so Close can wait for it
//
// An earlier version had only the first. It released the lock and did the cleanup itself, so this
// sequence was possible:
//
//	notifyPressure: active check passes, unlock
//	Close:          timer = nil, pressure.Store(None), Close RETURNS
//	notifyPressure: releaseMemory() -> Store(Critical), cache flush, badCleanup, FreeOSMemory
//
// A torn-down service was left reporting Critical with cleanup still running.
//
// # Why the work is queued instead of executed here
//
// The cleanup is destructive: it flushes the cache, forces a GC, and publishes Critical. A
// threshold poll can be doing the same thing at the same moment - the dispatch source runs on its
// own queue - and two forced GCs for one pressure episode is the cost this executor exists to
// remove. Queueing joins the same single authority the timer uses, and the barrier then covers
// both the callback's state change and the queued action.
//
// The callback does NOT call poll(). It accelerates the next observation instead, by resetting the
// timer to the minimum interval. Calling poll synchronously here would make the callback a second
// observer whose sample and state transition overlap the timer's, and it is what produced the
// "periodic GC from a callback" shape the old code had.
func (t *adaptiveTimer) notifyPressure() {
	t.access.Lock()

	// Read the active state and decide under the same lock, so Close cannot stop the timer
	// between the check and the queue.
	if t.timer == nil || t.reclaimStopped {
		t.access.Unlock()
		return
	}

	// Record the pressure intent while still holding the lock, so the state Close observes is
	// consistent and the next poll measures growth from the event rather than from an arbitrary
	// earlier sample.
	t.forceMinInterval = true
	t.pendingPressureBaseline = true

	// Joining the executor happens under the lock. stop() sets the stopped flag under this same
	// lock before it waits, so a callback either enqueued before that (and its barrier token is
	// observed) or sees the flag and never enqueues.
	t.submitPressureLocked()

	// Accelerate the observation without calling poll() from the callback. If a poll is already in
	// flight this Reset only schedules another; the observation sequence guard keeps their state
	// transitions ordered.
	if t.timer != nil {
		t.timer.Reset(t.minInterval)
	}

	t.access.Unlock()
}
