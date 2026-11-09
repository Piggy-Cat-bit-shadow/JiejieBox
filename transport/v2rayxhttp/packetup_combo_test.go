package v2rayxhttp

import (
	"bytes"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// Combined acceptance for the Box-shaped budget: TWO XHTTP clients bound to ONE shared
// upload budget (what box.go assembles), a slow/gated network, bulk waiters from BOTH
// clients competing for the same pool, and a new stream's opening that must reach the
// wire while every data POST is still held.
//
// The component tests cover each half; this one is the interaction the fault matrix asks
// for - the shared bound, the per-client per-stream bound and the control guarantee
// under simultaneous pressure from more than one outbound.

// TestPacketUpCombinedBoxBudgetOpensUnderBulkPressure runs that shape at the production
// pool sizes for ScMaxEachPostBytes=16384: capacity 256 KiB, control 128 KiB, reserve
// 128 KiB.
func TestPacketUpCombinedBoxBudgetOpensUnderBulkPressure(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	var gateOnce sync.Once
	openGate := func() { gateOnce.Do(func() { close(gate) }) }
	firstPostFrom := func(transport *probeTransport, signal chan<- struct{}) {
		transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
			select {
			case signal <- struct{}{}:
			default:
			}
			<-gate
			return okProbeResponse(), nil
		}
	}
	transportA := newProbeTransport()
	transportB := newProbeTransport()
	postedA := make(chan struct{}, 64)
	postedB := make(chan struct{}, 64)
	firstPostFrom(transportA, postedA)
	firstPostFrom(transportB, postedB)

	clientA := budgetedBatchClient(t, transportA, metaOptions{ScMaxEachPostBytes: "16384-16384"})
	clientB := budgetedBatchClient(t, transportB, metaOptions{ScMaxEachPostBytes: "16384-16384"})
	shared := clientA.NewUploadBudget()
	if shared == nil {
		t.Fatal("clientA has no own budget to size a shared one from")
	}
	if err := shared.Bind(clientA); err != nil {
		t.Fatalf("Bind clientA: %v", err)
	}
	if err := shared.Bind(clientB); err != nil {
		t.Fatalf("Bind clientB: %v", err)
	}
	if clientA.UploadBudget() != clientB.UploadBudget() {
		t.Fatal("the two clients are not on one shared budget")
	}
	pool := shared.pool
	stats := pool.stats()
	if stats.Capacity != 256*1024 || stats.Control != 128*1024 {
		t.Fatalf("unexpected pool sizing: %+v", stats)
	}

	// Four streams on client A fill the shared main pool.
	fill := make([]net.Conn, 0, 4)
	for index := 0; index < 4; index++ {
		conn := dialBatchConn(t, clientA)
		fill = append(fill, conn)
		if result := awaitPayloadWrite(t, writePayloadAsync(conn, make([]byte, 64*1024)), 64*1024); result.err != nil || result.n != 64*1024 {
			t.Fatalf("fill stream %d Write = (%d, %v)", index, result.n, result.err)
		}
	}
	waitForPool(t, pool, "shared main pool full", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})

	// Bulk waiters from BOTH clients compete for the same pool. Larger than the short
	// class, so only the main pool can admit them.
	bulk := make([]<-chan writeResult, 0, 6)
	all := append([]net.Conn(nil), fill...)
	for index := 0; index < 6; index++ {
		client := clientA
		if index%2 == 1 {
			client = clientB
		}
		conn := dialBatchConn(t, client)
		all = append(all, conn)
		bulk = append(bulk, writePayloadAsync(conn, bytes.Repeat([]byte{'B'}, 300*1024)))
	}
	waitForPool(t, pool, "bulk waiters from both clients", func(stats uplinkBudgetStats) bool {
		return stats.Waiters == 6
	})
	if stats := pool.stats(); stats.AllocWaiters != 0 {
		t.Fatalf("%d writers wait for array capacity; they should wait for the main pool", stats.AllocWaiters)
	}

	// The opening from client B: 1 KiB must reach the wire while the main pool is full
	// and every data POST (including the four fill posts from A) is held.
	opener := dialBatchConn(t, clientB)
	all = append(all, opener)
	if result := awaitPayloadWrite(t, writePayloadAsync(opener, bytes.Repeat([]byte{'o'}, 1024)), 1024); result.err != nil || result.n != 1024 {
		t.Fatalf("opening Write = (%d, %v)", result.n, result.err)
	}
	select {
	case <-postedB:
	case <-time.After(2 * time.Second):
		last := pool.stats()
		t.Fatalf("the opening never reached client B's transport while the shared main pool was full: %+v", last)
	}
	admitted := pool.stats()
	if admitted.Used != admitted.Capacity {
		t.Fatalf("main pool %d after the opening, want it still full at %d", admitted.Used, admitted.Capacity)
	}
	if admitted.ControlUsed < 1024 {
		t.Fatalf("the opening did not charge the control pool: %+v", admitted)
	}
	if admitted.Allocated > admitted.AllocCapacity {
		t.Fatalf("allocated %d exceeds the bound %d", admitted.Allocated, admitted.AllocCapacity)
	}
	if admitted.Streams != 11 {
		t.Fatalf("shared budget sees %d streams, want 11 (4 fill + 6 bulk + opener)", admitted.Streams)
	}

	// Release: every writer finishes, both clients' streams drain, and the one shared
	// ledger returns to zero.
	openGate()
	for _, write := range bulk {
		if result := awaitPayloadWrite(t, write, 300*1024); result.err != nil || result.n != 300*1024 {
			t.Fatalf("bulk Write = (%d, %v)", result.n, result.err)
		}
	}
	waitForPool(t, pool, "shared budget drained", func(stats uplinkBudgetStats) bool {
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
		t.Fatalf("ledger defects: %+v", final)
	}
	if final.GrantedBytes != final.ReleasedBytes || final.AllocGrantedBytes != final.AllocReleasedBytes {
		t.Fatalf("ledgers do not close: %+v", final)
	}
}

// TestPacketUpCombinedBoxBudgetIsolatesTwoBoxes is the isolation half of the same
// shape: a saturated Box must not delay another Box's opening. Two budgets, one
// saturated via a gated transport, the other opened while the first is held.
func TestPacketUpCombinedBoxBudgetIsolatesTwoBoxes(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	freeGate := make(chan struct{})
	transportFull := newProbeTransport()
	transportFree := newProbeTransport()
	transportFull.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		<-gate
		return okProbeResponse(), nil
	}
	freePosted := make(chan struct{}, 1)
	// The second Box's post is held too, so its charge is still on its ledger when the
	// assertions run: an un-held post would complete and release before the snapshot,
	// which is a property of the scheduler, not of the isolation claim.
	transportFree.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		select {
		case freePosted <- struct{}{}:
		default:
		}
		<-freeGate
		return okProbeResponse(), nil
	}

	fullClientA := budgetedBatchClient(t, transportFull, metaOptions{ScMaxEachPostBytes: "8192-8192"})
	fullClientB := budgetedBatchClient(t, transportFull, metaOptions{ScMaxEachPostBytes: "8192-8192"})
	fullBudget := fullClientA.NewUploadBudget()
	if err := fullBudget.Bind(fullClientA); err != nil {
		t.Fatalf("Bind A: %v", err)
	}
	if err := fullBudget.Bind(fullClientB); err != nil {
		t.Fatalf("Bind B: %v", err)
	}
	otherClient := budgetedBatchClient(t, transportFree, metaOptions{ScMaxEachPostBytes: "8192-8192"})
	otherBudget := otherClient.NewUploadBudget()
	if err := otherBudget.Bind(otherClient); err != nil {
		t.Fatalf("Bind other: %v", err)
	}
	if fullBudget.pool == otherBudget.pool {
		t.Fatal("two Boxes share one pool")
	}

	// Saturate the first Box: four 64 KiB writes fill its 256 KiB main pool.
	for index := 0; index < 4; index++ {
		conn := dialBatchConn(t, fullClientA)
		if result := awaitPayloadWrite(t, writePayloadAsync(conn, make([]byte, 64*1024)), 64*1024); result.err != nil || result.n != 64*1024 {
			t.Fatalf("fill Write %d = (%d, %v)", index, result.n, result.err)
		}
	}
	waitForPool(t, fullBudget.pool, "first Box saturated", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})
	blocked := writePayloadAsync(dialBatchConn(t, fullClientB), bytes.Repeat([]byte{'x'}, 300*1024))
	waitForPool(t, fullBudget.pool, "first Box has a waiter", func(stats uplinkBudgetStats) bool {
		return stats.Waiters == 1
	})

	// The second Box opens while the first is held: its own pool, its own post.
	other := dialBatchConn(t, otherClient)
	if result := awaitPayloadWrite(t, writePayloadAsync(other, bytes.Repeat([]byte{'y'}, 1024)), 1024); result.err != nil || result.n != 1024 {
		t.Fatalf("second Box opening Write = (%d, %v)", result.n, result.err)
	}
	select {
	case <-freePosted:
	case <-time.After(2 * time.Second):
		t.Fatal("the second Box's opening never reached its transport while the first Box was saturated")
	}
	otherStats := otherBudget.Snapshot()
	if otherStats.Used == 0 {
		t.Fatalf("the second Box was not charged for its own opening: %+v", otherStats)
	}
	if otherStats.Streams != 1 {
		t.Fatalf("the second Box holds %d streams, want only its own opening", otherStats.Streams)
	}
	// Isolation is an identity claim, not an equality one: the first Box's ledger must
	// contain exactly its own five streams (four fill + one blocked) and none of the
	// second Box's traffic.
	fullStats := fullBudget.Snapshot()
	if fullStats.Streams != 5 {
		t.Fatalf("the first Box holds %d streams, want its own five: %+v", fullStats.Streams, fullStats)
	}
	if fullStats.ControlUsed != 0 {
		t.Fatalf("the second Box's opening leaked into the first Box's control pool: %+v", fullStats)
	}

	close(gate)
	close(freeGate)
	if result := awaitPayloadWrite(t, blocked, 300*1024); result.err != nil || result.n != 300*1024 {
		t.Fatalf("blocked Write = (%d, %v)", result.n, result.err)
	}

	waitForPool(t, fullBudget.pool, "first Box drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
	waitForPool(t, otherBudget.pool, "second Box drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
}
