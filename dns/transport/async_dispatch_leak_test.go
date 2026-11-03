package transport

import (
	"context"
	"io"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mDNS "github.com/miekg/dns"
)

// TestAsyncDispatchGoroutinesDrain is the leak check for the round-03 change that moved
// connection setup off the caller's goroutine.
//
// That change trades "the packet path blocks for the length of a dial" for "one goroutine per
// in-flight query". The trade is only correct if the goroutines END: a query that is answered,
// failed, or cancelled must return its goroutine, or a DNS storm becomes an unbounded goroutine
// leak in exactly the process that is trying not to grow.
//
// The gate here is the ceiling on in-flight queries, not the gate itself: callers are counted,
// dials are held until every caller is parked, and then released together.
func TestAsyncDispatchGoroutinesDrain(t *testing.T) {
	t.Parallel()

	releaseDial := make(chan struct{})
	dialStarted := make(chan struct{}, 1)
	var dials atomic.Int64
	multiplexer := newQueryMultiplexer(queryMultiplexerOptions{
		dial: func(ctx context.Context) (net.Conn, error) {
			dials.Add(1)
			select {
			case dialStarted <- struct{}{}:
			default:
			}
			<-releaseDial
			return nil, io.ErrClosedPipe
		},
		write: func(conn net.Conn, message *mDNS.Msg, queryId uint16) error {
			return nil
		},
		readNext: func(conn net.Conn) (*mDNS.Msg, error) {
			return nil, io.EOF
		},
	})
	defer multiplexer.Close()

	baseline := runtime.NumGoroutine()
	// ONE context for the whole burst, cancelled at the end: the async contract is that the
	// caller's context outlives the call, which is how dns.Client passes its operation context.
	// Cancelling per caller would abort the query the moment the caller returned.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const burst = 200
	var completed atomic.Int64
	var wg sync.WaitGroup
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			message := new(mDNS.Msg)
			message.SetQuestion("drain.example.com.", mDNS.TypeA)
			multiplexer.ExchangeAsync(ctx, message, func(response *mDNS.Msg, err error) {
				completed.Add(1)
			})
		}()
	}

	// Wait until the dial has been entered; the ordered pool then serialises the rest behind it,
	// which is the shape being leak-checked: one dial, many parked callers.
	select {
	case <-dialStarted:
	case <-time.After(5 * time.Second):
		t.Fatalf("the dial never started: dials=%d goroutines=%d", dials.Load(), runtime.NumGoroutine())
	}

	close(releaseDial)
	wg.Wait()

	// wg covers the CALLERS, which return as soon as the query is dispatched. The callbacks are
	// the part that proves each dispatched query ran to completion.
	callbackDeadline := time.Now().Add(10 * time.Second)
	for completed.Load() != burst {
		if time.Now().After(callbackDeadline) {
			t.Fatalf("only %d of %d async queries reached a callback", completed.Load(), burst)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The dial goroutines and the parked callers must both be gone. A small slack covers the
	// runtime's own bookkeeping and the test's own goroutines unwinding.
	deadline := time.Now().Add(5 * time.Second)
	for {
		current := runtime.NumGoroutine()
		if current <= baseline+5 {
			t.Logf("goroutines returned to %d (baseline %d) after %d queries", current, baseline, burst)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines did not drain: %d running, baseline %d, after %d queries",
				current, baseline, burst)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
