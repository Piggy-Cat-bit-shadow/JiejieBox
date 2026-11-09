package v2rayxhttp

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// The allocation ledger (packetup_alloc.go), from the real packet-up write and POST
// path. The audit's P1-B is the first test: the logical pool alone cannot bound the
// queue's memory, because a POST and the queue behind it can hold one array while a
// second stream holds another.
//
// Every test keeps the logical ledger as a second, separate set of assertions: the two
// ledgers answer different questions and one must not be read as the other.

// allocationPostScenario is the P1-B fixture: one 16 KiB POST per batch, the first
// POST succeeds, every later one is held by a barrier until the test releases it.
type allocationPostScenario struct {
	transport *probeTransport
	gate      chan struct{}
	second    chan struct{}
	once      sync.Once
}

func newAllocationPostScenario(t *testing.T, capacity, reserve int) (*allocationPostScenario, *Client) {
	t.Helper()
	return newAllocationPostScenarioWithPost(t, capacity, reserve, "16384-16384")
}

func newAllocationPostScenarioWithPost(t *testing.T, capacity, reserve int, postSize string) (*allocationPostScenario, *Client) {
	t.Helper()
	scenario := &allocationPostScenario{
		transport: newProbeTransport(),
		gate:      make(chan struct{}),
		second:    make(chan struct{}),
	}
	scenario.transport.onPost = func(_ *http.Request, body []byte) (*http.Response, error) {
		var posts int
		scenario.transport.access.Lock()
		posts = scenario.transport.posts
		scenario.transport.access.Unlock()
		if posts == 1 {
			return okProbeResponse(), nil
		}
		scenario.once.Do(func() { close(scenario.second) })
		<-scenario.gate
		return okProbeResponse(), nil
	}
	client := budgetedBatchClientWith(t, scenario.transport, metaOptions{ScMaxEachPostBytes: postSize}, capacity, reserve)
	return scenario, client
}

// waitForSecondPost blocks until the stream's second POST is inside the transport,
// i.e. its first POST has returned and its charge has been released.
func (s *allocationPostScenario) waitForSecondPost(t *testing.T) {
	t.Helper()
	select {
	case <-s.second:
	case <-time.After(writeFreeBudget):
		t.Fatal("the stream never started its second POST")
	}
}

// release lets every gated POST finish and waits for the queues to drain.
func (s *allocationPostScenario) release(t *testing.T, pool *sharedUplinkBudget) {
	t.Helper()
	close(s.gate)
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
}

// requireAllocationLedger checks the capacity ledger's own conservation and bounds.
func requireAllocationLedger(t *testing.T, stats uplinkBudgetStats, where string) {
	t.Helper()
	if stats.Allocated < 0 || stats.Allocated > stats.AllocCapacity {
		t.Fatalf("%s: allocated capacity %d outside [0, %d]: %+v", where, stats.Allocated, stats.AllocCapacity, stats)
	}
	if stats.AllocPeak < stats.Allocated {
		t.Fatalf("%s: allocation peak %d is below the live allocation %d", where, stats.AllocPeak, stats.Allocated)
	}
	if got := stats.AllocGrantedBytes - stats.AllocReleasedBytes; got != uint64(stats.Allocated) {
		t.Fatalf("%s: alloc granted-released = %d, want the live allocation %d", where, got, stats.Allocated)
	}
}

// TestPacketUpAllocationLedgerCountsASharedArrayOnce is the audit's P1-B at the
// resource head.
//
// Main pool 64 KiB, 16 KiB POSTs. A writes 64 KiB; its first POST completes and the
// second is held, so the queue suffix and the POST share A's array while 48 KiB is
// still charged logically. B is then admitted for 16 KiB. The logical pool reports
// 64 KiB; the arrays are A's 64 KiB plus B's 16 KiB = 80 KiB. The new ledger must
// report the 80 KiB, must count A's shared array once, and must attribute each array
// to its owner.
func TestPacketUpAllocationLedgerCountsASharedArrayOnce(t *testing.T) {
	t.Parallel()

	const (
		capacity = 64 * 1024
		reserve  = 16 * 1024
	)
	scenario, client := newAllocationPostScenario(t, capacity, reserve)
	pool := client.budget
	bulk := dialBatchConn(t, client)

	if result := awaitPayloadWrite(t, writePayloadAsync(bulk, make([]byte, capacity)), capacity); result.err != nil || result.n != capacity {
		t.Fatalf("bulk Write = (%d, %v), want (%d, nil)", result.n, result.err, capacity)
	}
	scenario.waitForSecondPost(t)

	// B is admitted for 16 KiB out of the main pool the first POST released.
	next := dialBatchConn(t, client)
	chunk := bytes.Repeat([]byte("B"), 16*1024)
	if result := awaitPayloadWrite(t, writePayloadAsync(next, chunk), len(chunk)); result.err != nil || result.n != len(chunk) {
		t.Fatalf("second stream Write = (%d, %v), want (%d, nil)", result.n, result.err, len(chunk))
	}

	stats := pool.stats()
	// The logical ledger is unchanged: that is what the pre-fix pool reported.
	if stats.Total() != capacity {
		t.Fatalf("logical charge %d, want %d (the audit's 64 KiB): %+v", stats.Total(), capacity, stats)
	}
	// The capacity ledger sees both arrays, each once.
	if stats.Allocated != capacity+len(chunk) {
		t.Fatalf("allocated capacity %d, want %d (A's 64 KiB array + B's 16 KiB array): %+v",
			stats.Allocated, capacity+len(chunk), stats)
	}
	requireAllocationLedger(t, stats, "shared array")

	bulkStats := bulk.(*packetConn).uplink.stats()
	if bulkStats.AllocatedCapacity != capacity {
		t.Fatalf("A's allocation %d, want its 64 KiB array counted once (pending %d + in-flight %d = %d logical)",
			bulkStats.AllocatedCapacity, bulkStats.PendingBytes, bulkStats.InFlightBytes,
			bulkStats.PendingBytes+bulkStats.InFlightBytes)
	}
	if bulkStats.PendingBytes+bulkStats.InFlightBytes >= bulkStats.AllocatedCapacity {
		t.Fatalf("A's logical bytes (%d) are not below its array capacity (%d): the fixture no longer shows the gap",
			bulkStats.PendingBytes+bulkStats.InFlightBytes, bulkStats.AllocatedCapacity)
	}
	nextStats := next.(*packetConn).uplink.stats()
	if nextStats.AllocatedCapacity != len(chunk) {
		t.Fatalf("B's allocation %d, want its %d-byte array", nextStats.AllocatedCapacity, len(chunk))
	}
	if got := bulkStats.AllocatedCapacity + nextStats.AllocatedCapacity; got != stats.Allocated {
		t.Fatalf("per-stream allocations sum to %d but the pool reports %d: an array is double counted or unowned", got, stats.Allocated)
	}

	scenario.release(t, pool)
	for _, conn := range []net.Conn{bulk, next} {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	final := waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if final.Total() != 0 || final.Allocated != 0 {
		t.Fatalf("pool not back at baseline: %+v", final)
	}
	if final.AllocGrantedBytes != final.AllocReleasedBytes || final.LeftoverAllocBytes != 0 {
		t.Fatalf("allocation ledger does not close: %+v", final)
	}
}

// TestPacketUpAllocationLedgerChargesBothArraysDuringAGrowth pins the replacement
// window: when the queue grows while a POST owns the old array, both arrays are alive
// and both are charged. The pool must report the overlap in its peak, and the old
// array's capacity must come back when its last reference (the POST) returns.
//
// The fixture needs a queue array SMALLER than the per-stream bound, otherwise every
// append fits and no replacement happens: a 16 KiB write allocates a 16 KiB array, an
// 8 KiB POST leaves 8 KiB of pending in it, and the next 32 KiB write does not fit.
func TestPacketUpAllocationLedgerChargesBothArraysDuringAGrowth(t *testing.T) {
	t.Parallel()

	scenario, client := newAllocationPostScenarioWithPost(t, 256*1024, 16*1024, "8192-8192")
	pool := client.budget
	conn := dialBatchConn(t, client)

	const first = 16 * 1024
	if result := awaitPayloadWrite(t, writePayloadAsync(conn, make([]byte, first)), first); result.err != nil || result.n != first {
		t.Fatalf("first Write = (%d, %v)", result.n, result.err)
	}
	scenario.waitForSecondPost(t)
	before := pool.stats()
	if before.Allocated != first {
		t.Fatalf("allocated %d before the growth, want the %d-byte array", before.Allocated, first)
	}

	// 32 KiB cannot fit the 16 KiB array (8 KiB pending, 8 KiB in the gated POST), so
	// the queue is replaced while the POST still holds the old array.
	const second = 32 * 1024
	if result := awaitPayloadWrite(t, writePayloadAsync(conn, make([]byte, second)), second); result.err != nil || result.n != second {
		t.Fatalf("growth Write = (%d, %v)", result.n, result.err)
	}
	stats := pool.stats()
	if stats.Allocated <= first {
		t.Fatalf("allocated %d after growing a 16 KiB array: the old array is not charged alongside the new one", stats.Allocated)
	}
	requireAllocationLedger(t, stats, "replacement")
	if stats.AllocPeak < stats.Allocated {
		t.Fatalf("allocation peak %d below the live overlap %d", stats.AllocPeak, stats.Allocated)
	}
	streamStats := conn.(*packetConn).uplink.stats()
	if streamStats.AllocatedCapacity != stats.Allocated {
		t.Fatalf("stream allocation %d != pool allocation %d", streamStats.AllocatedCapacity, stats.Allocated)
	}
	if streamStats.RetainedCapacity < streamStats.PendingBytes {
		t.Fatalf("reported array capacity %d is below the queued %d bytes", streamStats.RetainedCapacity, streamStats.PendingBytes)
	}

	scenario.release(t, pool)
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	final := waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if final.Allocated != 0 || final.AllocGrantedBytes != final.AllocReleasedBytes || final.LeftoverAllocBytes != 0 {
		t.Fatalf("allocation ledger does not close: %+v", final)
	}
}

// TestPacketUpAllocationLedgerSettlesOnAFailedPost is the failure path: the array a
// failed POST owned, plus the queue behind it, must be returned when the failure
// settles, not when the session is closed.
func TestPacketUpAllocationLedgerSettlesOnAFailedPost(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	gate := make(chan struct{})
	refused := errors.New("upload refused")
	var once sync.Once
	transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		var first bool
		once.Do(func() { first = true })
		if first {
			<-gate
			return nil, refused
		}
		return okProbeResponse(), nil
	}
	client := budgetedBatchClientWith(t, transport, metaOptions{ScMaxEachPostBytes: "16384-16384"}, 64*1024, 16*1024)
	pool := client.budget
	conn := dialBatchConn(t, client)
	payload := bytes.Repeat([]byte("f"), 64*1024)
	if result := awaitPayloadWrite(t, writePayloadAsync(conn, payload), len(payload)); result.err != nil || result.n != len(payload) {
		t.Fatalf("Write = (%d, %v)", result.n, result.err)
	}
	waitForPool(t, pool, "allocated", func(stats uplinkBudgetStats) bool {
		return stats.Allocated >= len(payload)
	})
	close(gate)

	deadline := time.Now().Add(writeFreeBudget)
	for {
		stats := pool.stats()
		if stats.Allocated == 0 && stats.Total() == 0 {
			requireAllocationLedger(t, stats, "failed post")
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the failed post the ledgers still hold logical=%d allocated=%d", stats.Total(), stats.Allocated)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
}

// TestPacketUpAllocationLedgerOutlivesClose pins the late return: a POST wedged past
// Close's drain budget keeps its array (and its charge) until it returns, and then the
// capacity ledger returns to baseline exactly once.
func TestPacketUpAllocationLedgerOutlivesClose(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	gate := make(chan struct{})
	entered := make(chan struct{})
	transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-gate
		return okProbeResponse(), nil
	}
	client := budgetedBatchClientWith(t, transport, metaOptions{}, 64*1024, 8*1024)
	pool := client.budget
	conn := dialBatchConn(t, client)
	payload := bytes.Repeat([]byte("l"), 64*1024)
	if result := awaitPayloadWrite(t, writePayloadAsync(conn, payload), len(payload)); result.err != nil || result.n != len(payload) {
		t.Fatalf("Write = (%d, %v)", result.n, result.err)
	}
	select {
	case <-entered:
	case <-time.After(writeFreeBudget):
		t.Fatal("the post never started")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	held := pool.stats()
	if held.Allocated == 0 {
		t.Fatalf("the allocation was returned while its POST is still wedged: %+v", held)
	}
	requireAllocationLedger(t, held, "after a timed-out Close")

	close(gate)
	waitForPool(t, pool, "late settlement", func(stats uplinkBudgetStats) bool {
		return stats.Allocated == 0 && stats.Total() == 0
	})
	final := waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if final.AllocGrantedBytes != final.AllocReleasedBytes || final.LeftoverAllocBytes != 0 {
		t.Fatalf("allocation ledger does not close after the late return: %+v", final)
	}
}

// TestPacketUpAllocationLedgerBoundsAnOversizedWrite covers the single write larger
// than the whole queue: it is appended in chunks, each growth goes through the ledger,
// and the controlled capacity stays under its bound while the write still completes.
func TestPacketUpAllocationLedgerBoundsAnOversizedWrite(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	transport.responseDelay = 50 * time.Microsecond
	// maxPending is the 64 KiB floor; the write is four times that.
	client := budgetedBatchClientWith(t, transport, metaOptions{ScMaxEachPostBytes: "16384-16384"}, 64*1024, 16*1024)
	pool := client.budget
	conn := dialBatchConn(t, client)
	uplink := conn.(*packetConn).uplink

	const total = 4 * 64 * 1024
	written := make(chan writeResult, 1)
	go func() {
		n, err := conn.Write(bytes.Repeat([]byte("w"), total))
		written <- writeResult{n: n, err: err}
	}()
	peak := 0
	deadline := time.Now().Add(writeFreeBudget)
	for done := false; !done; {
		select {
		case result := <-written:
			if result.err != nil || result.n != total {
				t.Fatalf("oversized Write = (%d, %v), want (%d, nil)", result.n, result.err, total)
			}
			done = true
		default:
			stats := pool.stats()
			requireAllocationLedger(t, stats, "oversized write")
			if stats.Allocated > peak {
				peak = stats.Allocated
			}
			if queued := uplink.stats().PendingBytes; queued > uplink.maxPending {
				t.Fatalf("pending %d over the per-stream bound %d", queued, uplink.maxPending)
			}
			if time.Now().After(deadline) {
				t.Fatal("the oversized write never completed")
			}
			time.Sleep(time.Millisecond)
		}
	}
	if peak == 0 {
		t.Fatal("the allocation ledger never charged anything")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
}

// TestPacketUpAllocationLedgerUnderConcurrency is the -race and conservation target:
// several streams grow and drain their queues at once against a small ledger, sampling
// the bounds throughout, then returning everything.
func TestPacketUpAllocationLedgerUnderConcurrency(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	transport.responseDelay = 100 * time.Microsecond
	client := budgetedBatchClientWith(t, transport, metaOptions{ScMaxEachPostBytes: "8192-8192"}, 32*1024, 8*1024)
	pool := client.budget
	const streams = 4
	conns := make([]net.Conn, 0, streams)
	for index := 0; index < streams; index++ {
		conns = append(conns, dialBatchConn(t, client))
	}
	var waitGroup sync.WaitGroup
	for index, conn := range conns {
		waitGroup.Add(1)
		go func(conn net.Conn, seed int) {
			defer waitGroup.Done()
			for round := 0; round < 10; round++ {
				size := 1 + (seed*97+round*31)%(12*1024)
				if round%4 == 0 {
					size += 24 * 1024
				}
				if _, err := conn.Write(bytes.Repeat([]byte{'a'}, size)); err != nil {
					t.Errorf("concurrent Write: %v", err)
					return
				}
			}
		}(conn, index)
	}
	done := make(chan struct{})
	go func() { waitGroup.Wait(); close(done) }()
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			stats := pool.stats()
			requireAllocationLedger(t, stats, "concurrency")
			time.Sleep(100 * time.Microsecond)
		}
	}
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
	for _, conn := range conns {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	final := waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if final.AllocGrantedBytes != final.AllocReleasedBytes || final.LeftoverAllocBytes != 0 || final.ExcessReleases != 0 {
		t.Fatalf("allocation ledger does not close: %+v", final)
	}
}

// TestPacketUpQueueCapacityPolicy pins the growth policy as a pure function: it must
// always cover the need, grow geometrically for small queues, step by a bounded amount
// for large ones, and round up to the ledger's granule.
func TestPacketUpQueueCapacityPolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		current int
		need    int
	}{
		{"new_queue", 0, 1},
		{"new_queue_16k", 0, 16 * 1024},
		{"new_queue_odd", 0, 16*1024 + 1},
		{"grow_small", 32 * 1024, 33 * 1024},
		{"grow_geometric", 64 * 1024, 65 * 1024},
		{"grow_large_steps", 4 << 20, (4 << 20) + 8},
	}
	for _, testCase := range cases {
		capacity := packetUpQueueCapacity(testCase.current, testCase.need)
		if capacity < testCase.need {
			t.Fatalf("%s: capacity %d does not cover need %d", testCase.name, capacity, testCase.need)
		}
		if capacity%packetUpAllocGranule != 0 {
			t.Fatalf("%s: capacity %d is not a multiple of the %d-byte granule", testCase.name, capacity, packetUpAllocGranule)
		}
		if testCase.current > 0 {
			if capacity > testCase.current+packetUpAllocMaxStep+packetUpAllocGranule {
				t.Fatalf("%s: capacity %d grew more than one bounded step from %d", testCase.name, capacity, testCase.current)
			}
		}
	}
	// The geometric part is what keeps the copying linear: a queue doubling must be
	// able to grow by less than the need alone.
	if got := packetUpQueueCapacity(64*1024, 65*1024); got != 96*1024 {
		t.Fatalf("geometric growth = %d, want 96 KiB (1.5x the current capacity)", got)
	}
	if got := packetUpQueueCapacity(0, 16*1024); got != 16*1024 {
		t.Fatalf("a 16 KiB queue allocated %d, want it rounded to the granule only", got)
	}
	if got := packetUpQueueCapacity(0, 0); got != 0 {
		t.Fatalf("empty need allocated %d", got)
	}
}
