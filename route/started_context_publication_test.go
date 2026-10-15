package route

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTheStartedContextIsPublishedForConcurrentReaders pins the contract that replaced a plain field
// with an atomic publication.
//
// # The race this exists for
//
// `startedCtx` was a plain field, written once by Start on the goroutine running the start sequence
// and read by threads this process does not own. The Phase-3 race sweep caught it:
//
//	WARNING: DATA RACE
//	  Write at route/network.go:391              r.startedCtx = scope.Context()
//	  Read  at route/network_environment.go:423  if r.startedCtx == nil
//	  ...  adapter/lifecycle.go:168, box.go:1051
//	  ...  sing-tun monitor_windows.go:47  <- the winipcfg interface-change handler's thread
//
// That is the shape the field's comment describes: the interface monitor is STARTED in the same
// Start call, earlier in the same switch, so an interface event delivered in that window reads a
// field the start goroutine is concurrently writing.
//
// # Why this test is a concurrent publication rather than a value assertion
//
// The defect was never a wrong value - both orderings produce a usable context or nil, and the
// reader tolerates nil. It was the absence of a happens-before edge. A sequential test cannot see
// that and would have passed against the buggy code, which is exactly why the race reached a
// release candidate. So the test drives the two sides CONCURRENTLY and relies on -race to judge it:
// under `go test -race` this fails on a plain field and passes on an atomic one.
//
// The assertion at the end is only a liveness check - it proves the readers ran - so the test is
// meaningful with and without the detector, and strict with it.
func TestTheStartedContextIsPublishedForConcurrentReaders(t *testing.T) {
	manager := &NetworkManager{}
	ctx := t.Context()

	// A reader standing in for the platform's interface callback: it reads the publication the way
	// environmentTransitionApplies does, including through a cancelled context, which is the state
	// Close leaves behind.
	readEnded := make(chan struct{})
	var readCount int
	var readAccess sync.Mutex
	reader := func() {
		defer close(readEnded)
		for i := 0; i < 1000; i++ {
			if published := manager.startedContext(); published != nil {
				_ = published.Err()
			}
			readAccess.Lock()
			readCount++
			readAccess.Unlock()
		}
	}

	go reader()
	// The writer, on this goroutine, exactly as Start does it.
	for i := 0; i < 100; i++ {
		manager.publishStartedContext(ctx)
	}
	<-readEnded

	readAccess.Lock()
	defer readAccess.Unlock()
	require.Equal(t, 1000, readCount,
		"the readers must actually have run, or this test would pass without testing anything")
	require.NotNil(t, manager.startedContext(),
		"the publication must be visible after the write")
	require.NoError(t, manager.startedContext().Err(),
		"and it is the live run's context")
}

// TestAnUnstartedManagerReportsNoContext is the negative half, and the reason the accessor returns
// nil rather than a background context.
//
// `environmentTransitionApplies` turns "no context" into "no transition applies", and the field's
// comment explains why the sentinel cannot be a zero fingerprint: 0 is a real, representable
// environment. A refactor that returned context.Background() here would silently make every
// pre-start observation look like a transition on a started manager.
func TestAnUnstartedManagerReportsNoContext(t *testing.T) {
	manager := &NetworkManager{}

	require.Nil(t, manager.startedContext(),
		"a manager that has not been started has no run, and must say so rather than inventing one")

	// And the published-then-cleared state, which is what the initial-environment test drives: a
	// context that was published and then cleared must read as absent again.
	manager.publishStartedContext(t.Context())
	require.NotNil(t, manager.startedContext())
	manager.startedCtx.Store(nil)
	require.Nil(t, manager.startedContext(),
		"clearing the publication must be observable, because the pre-start boundary is what the "+
			"first-observation gate keys on")
}
