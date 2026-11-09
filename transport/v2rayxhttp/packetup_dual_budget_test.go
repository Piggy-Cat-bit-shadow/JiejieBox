package v2rayxhttp

import (
	"bytes"
	"net"
	"net/http"
	"testing"
	"time"
)

// The dual-budget regression from the audit: the logical pools and the allocation
// ledger are two bounds, and before the fix a writer waiting for LOGICAL capacity had
// already reserved its queue array. Twelve bulk waiters therefore parked on 768 KiB of
// empty arrays, filled the allocation ledger exactly, and a new stream's opening could
// not get the 4 KiB array it needed even though the control quota had 126 KiB free.
//
// The fix is an order and a rollback: admit logically first, reserve the array for
// exactly what was admitted, and if the reservation fails hand the admission back
// before parking. A waiting writer now holds no memory.

// TestPacketUpOpeningSurvivesAnAllocationSaturatedLedger is the audit's reproduction,
// at the production pool sizes for ScMaxEachPostBytes=32768:
//
//	capacity 256 KiB, control 128 KiB, reserve 128 KiB, allocation bound 1032 KiB
//
// Four streams fill the main pool; twelve >256 KiB writes wait for the main pool (they
// must hold no array); two 1 KiB short flows take the last allocation granules; then a
// new stream's 1 KiB opening must reach the RoundTripper while every data POST is
// gated. Before the fix Allocated was the whole bound (1056768) and the opening waited
// in the allocation queue instead of reaching the transport.
func TestPacketUpOpeningSurvivesAnAllocationSaturatedLedger(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	gate := make(chan struct{})
	transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		<-gate
		return okProbeResponse(), nil
	}
	client := budgetedBatchClient(t, transport, metaOptions{ScMaxEachPostBytes: "32768"})
	pool := client.budget
	initial := pool.stats()
	if initial.Capacity != 256*1024 || initial.Control != 128*1024 || initial.Reserve != 128*1024 {
		t.Fatalf("unexpected production pool sizing: %+v", initial)
	}
	if initial.AllocCapacity != 1056768 {
		t.Fatalf("allocation bound %d, want the audit's 1056768", initial.AllocCapacity)
	}

	// 1. Four streams fill the 256 KiB main pool.
	bulk := make([]net.Conn, 0, 4)
	for index := 0; index < 4; index++ {
		conn := dialBatchConn(t, client)
		bulk = append(bulk, conn)
		if result := awaitPayloadWrite(t, writePayloadAsync(conn, make([]byte, 64*1024)), 64*1024); result.err != nil || result.n != 64*1024 {
			t.Fatalf("bulk stream %d Write = (%d, %v)", index, result.n, result.err)
		}
	}
	waitForPool(t, pool, "main pool full", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})
	if stats := pool.stats(); stats.Allocated != 4*64*1024 {
		t.Fatalf("the four main-pool arrays hold %d bytes, want %d", stats.Allocated, 4*64*1024)
	}

	// 2. Twelve long streams each issue a >256 KiB write. They must wait for the main
	// pool WITHOUT holding an empty queue array.
	all := make([]net.Conn, 0, 18)
	all = append(all, bulk...)
	long := make([]<-chan writeResult, 0, 12)
	for index := 0; index < 12; index++ {
		conn := dialBatchConn(t, client)
		all = append(all, conn)
		long = append(long, writePayloadAsync(conn, bytes.Repeat([]byte{'L'}, 300*1024)))
	}
	waitForPool(t, pool, "twelve bulk waiters", func(stats uplinkBudgetStats) bool {
		return stats.Waiters == 12
	})
	waiting := pool.stats()
	bulkWaiterHeld := waiting.Allocated - 4*64*1024
	if waiting.AllocWaiters != 0 {
		t.Fatalf("%d writers wait for array capacity; they should only be waiting for the main pool", waiting.AllocWaiters)
	}

	// 3. Two short flows spend the last two allocation granules.
	for index := 0; index < 2; index++ {
		conn := dialBatchConn(t, client)
		all = append(all, conn)
		if result := awaitPayloadWrite(t, writePayloadAsync(conn, make([]byte, 1024)), 1024); result.err != nil || result.n != 1024 {
			t.Fatalf("short flow %d Write = (%d, %v)", index, result.n, result.err)
		}
	}
	short := pool.stats()
	if short.Allocated > short.AllocCapacity {
		t.Fatalf("data allocations %d exceed the allocation bound %d", short.Allocated, short.AllocCapacity)
	}
	if short.ControlUsed != 2*1024 {
		t.Fatalf("the short flows charged %d control bytes, want 2048", short.ControlUsed)
	}

	// 4. The new stream's opening must reach the transport before the data POSTs are
	// released.
	opener := dialBatchConn(t, client)
	all = append(all, opener)
	opening := bytes.Repeat([]byte{'o'}, 1024)
	openDone := writePayloadAsync(opener, opening)
	select {
	case result := <-openDone:
		if result.err != nil || result.n != len(opening) {
			t.Fatalf("opening Write = (%d, %v), want (%d, nil)", result.n, result.err, len(opening))
		}
	case <-time.After(2 * time.Second):
		last := pool.stats()
		t.Fatalf("the new stream's 1 KiB opening never finished: bulk waiters held %d allocation bytes of their own, "+
			"ledger Allocated=%d/%d ControlUsed=%d/%d Waiters=%d AllocWaiters=%d",
			bulkWaiterHeld, last.Allocated, last.AllocCapacity, last.ControlUsed, last.Control,
			last.Waiters, last.AllocWaiters)
	}
	waitForSessionUpload(t, transport, opener.(*packetConn).sessionID, 1)
	if bulkWaiterHeld != 0 {
		t.Fatalf("bulk waiters held %d allocation bytes while waiting for the main pool; a waiting writer must hold no array",
			bulkWaiterHeld)
	}

	admitted := pool.stats()
	if admitted.ControlUsed < 3*1024 {
		t.Fatalf("control pool used %d after three openings, want at least 3072", admitted.ControlUsed)
	}
	if admitted.Used != admitted.Capacity {
		t.Fatalf("main pool %d after the opening, want it still full at %d", admitted.Used, admitted.Capacity)
	}
	if admitted.Allocated > admitted.AllocCapacity {
		t.Fatalf("allocated %d exceeds the allocation bound %d", admitted.Allocated, admitted.AllocCapacity)
	}

	// Release: every waiter must finish, and both ledgers return to zero.
	close(gate)
	for _, write := range long {
		if result := awaitPayloadWrite(t, write, 300*1024); result.err != nil || result.n != 300*1024 {
			t.Fatalf("long stream Write = (%d, %v), want (%d, nil)", result.n, result.err, 300*1024)
		}
	}
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
	for _, conn := range all {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	final := waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if final.ExcessReleases != 0 || final.LeftoverAllocBytes != 0 {
		t.Fatalf("ledger defects after the burst: %+v", final)
	}
	if final.AllocGrantedBytes != final.AllocReleasedBytes || final.GrantedBytes != final.ReleasedBytes {
		t.Fatalf("ledgers do not close: %+v", final)
	}
}

// TestPacketUpBulkWaitersHoldNoArray pins the same invariant without the full scenario:
// a writer whose logical admission is refused must not have charged the allocation
// ledger for an array, and it must not sit in the allocation waiters list either.
func TestPacketUpBulkWaitersHoldNoArray(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	gate := make(chan struct{})
	transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		<-gate
		return okProbeResponse(), nil
	}
	client := budgetedBatchClientWith(t, transport, metaOptions{ScMaxEachPostBytes: "8192-8192"}, 16*1024, 0)
	pool := client.budget
	first := dialBatchConn(t, client)
	if result := awaitPayloadWrite(t, writePayloadAsync(first, make([]byte, 16*1024)), 16*1024); result.err != nil || result.n != 16*1024 {
		t.Fatalf("first Write = (%d, %v)", result.n, result.err)
	}
	waitForPool(t, pool, "main full", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})
	before := pool.stats()
	blocked := make([]<-chan writeResult, 0, 6)
	for index := 0; index < 6; index++ {
		conn := dialBatchConn(t, client)
		// Larger than the short class: only the main pool can admit it.
		blocked = append(blocked, writePayloadAsync(conn, bytes.Repeat([]byte{'b'}, 300*1024)))
	}
	waitForPool(t, pool, "bulk waiters", func(stats uplinkBudgetStats) bool {
		return stats.Waiters == 6
	})
	after := pool.stats()
	if after.Allocated != before.Allocated {
		t.Fatalf("bulk waiters charged the allocation ledger: %d -> %d", before.Allocated, after.Allocated)
	}
	if after.AllocWaiters != 0 {
		t.Fatalf("%d bulk waiters parked in the allocation list", after.AllocWaiters)
	}
	close(gate)
	for _, write := range blocked {
		if result := awaitPayloadWrite(t, write, 300*1024); result.err != nil || result.n != 300*1024 {
			t.Fatalf("bulk waiter Write = (%d, %v)", result.n, result.err)
		}
	}
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
}

// TestPacketUpDoubleBudgetCloseReturnsEverything covers the shutdown path of the same
// two ledgers: after Close with a wedged POST, the arrays and both logical charges come
// back exactly once when the POST returns.
func TestPacketUpDoubleBudgetCloseReturnsEverything(t *testing.T) {
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
	client := budgetedBatchClientWith(t, transport, metaOptions{ScMaxEachPostBytes: "32768-32768"}, 256*1024, 64*1024)
	pool := client.budget
	conn := dialBatchConn(t, client)
	payload := make([]byte, 64*1024)
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
	wedged := pool.stats()
	if wedged.Allocated == 0 || wedged.Total() == 0 {
		t.Fatalf("Close released charges its wedged POST still owns: %+v", wedged)
	}
	close(gate)
	waitForPool(t, pool, "late settlement", func(stats uplinkBudgetStats) bool {
		return stats.Allocated == 0 && stats.Total() == 0
	})
	final := waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if final.AllocGrantedBytes != final.AllocReleasedBytes || final.GrantedBytes != final.ReleasedBytes {
		t.Fatalf("ledgers do not close: %+v", final)
	}
	if final.ExcessReleases != 0 || final.LeftoverAllocBytes != 0 {
		t.Fatalf("ledger defects: %+v", final)
	}
}
