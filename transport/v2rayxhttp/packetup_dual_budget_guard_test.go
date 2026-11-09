package v2rayxhttp

import (
	"bytes"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"
)

// The guards behind the dual-budget fix (packetup_dual_budget_test.go): the allocation
// ledger's control reserve, the grant/rollback pair, the replacement overlap bound and
// the retry path under a tight ledger.
//
// Some of these need a tighter bound than production sizing ever reaches, so they set
// the pool's limits directly (setAllocationLimits). That is a deliberate test seam: the
// production bound is derived and far above the logical aggregate, which means the
// "data saturates the ledger" case cannot occur in the field - but the guard must still
// hold if a future Box-level cap makes it reachable.

// setAllocationLimits replaces the derived allocation bound. Test-only.
func setAllocationLimits(pool *sharedUplinkBudget, total, controlReserve int) {
	pool.access.Lock()
	pool.allocCapacity = total
	pool.allocDataLimit = total - controlReserve
	pool.access.Unlock()
}

// TestAllocationReserveKeepsTheOpeningShare is the primitive: data may fill its limit,
// after which a data reservation is refused but a protected (opening) one still fits in
// the remaining total. It also pins the ledger's conservation across reserve/release.
func TestAllocationReserveKeepsTheOpeningShare(t *testing.T) {
	t.Parallel()

	pool := newSharedUplinkBudgetWithControl(64*1024, 16*1024, 32*1024)
	setAllocationLimits(pool, 48*1024, 16*1024) // data limit 32 KiB
	uplink := &packetUplink{wakeCh: make(chan struct{})}

	if granted := pool.reserveAllocation(uplink, 16*1024, false); granted != 16*1024 {
		t.Fatalf("data reservation 16 KiB granted %d", granted)
	}
	if granted := pool.reserveAllocation(uplink, 12*1024, false); granted != 12*1024 {
		t.Fatalf("data reservation 12 KiB granted %d", granted)
	}
	if granted := pool.reserveAllocation(uplink, 4*1024, false); granted != 4*1024 {
		t.Fatalf("data reservation up to the limit granted %d", granted)
	}
	if granted := pool.reserveAllocation(uplink, 4*1024, false); granted != 0 {
		t.Fatalf("data reservation past the data limit granted %d", granted)
	}
	stats := pool.stats()
	if stats.Allocated != 32*1024 || stats.AllocDataLimit != 32*1024 {
		t.Fatalf("ledger %+v after data reservations", stats)
	}
	if stats.AllocWaiters != 1 {
		t.Fatalf("a refused reservation must register the waiter, got %d", stats.AllocWaiters)
	}

	// An opening (protected) reservation may use the whole bound: 16 KiB left of 48.
	if granted := pool.reserveAllocation(uplink, 8*1024, true); granted != 8*1024 {
		t.Fatalf("protected reservation granted %d, want the opening reserve to cover it", granted)
	}
	if granted := pool.reserveAllocation(uplink, 8*1024, true); granted != 8*1024 {
		t.Fatalf("protected reservation granted %d, want the rest of the total", granted)
	}
	if granted := pool.reserveAllocation(uplink, 1024, true); granted != 0 {
		t.Fatalf("protected reservation past the total granted %d", granted)
	}

	pool.releaseAllocation(uplink, 16*1024)
	pool.releaseAllocation(uplink, 8*1024)
	stats = pool.stats()
	if stats.Allocated != 24*1024 {
		t.Fatalf("allocated %d after releases, want 24576", stats.Allocated)
	}
	pool.releaseAllocation(uplink, 16*1024)
	pool.releaseAllocation(uplink, 8*1024)
	stats = pool.stats()
	if stats.Allocated != 0 || stats.AllocOverlap != 0 {
		t.Fatalf("ledger not back at zero: %+v", stats)
	}
	if stats.AllocGrantedBytes-stats.AllocReleasedBytes != uint64(stats.Allocated) {
		t.Fatalf("allocation flow does not close: %+v", stats)
	}
}

// TestPacketUpGrantRollsBackWhenTheArrayCannotBeReserved pins the pair semantics: a
// writer that is admitted logically but cannot reserve its array returns the admission
// exactly before waiting, so the logical ledger shows no charge for bytes that were never
// queued, the writer parks quietly, and a real release lets it proceed.
//
// The write must be DATA-class for the reservation to fail while the logical pools have
// room: an opening write is allowed the control allocation reserve (that is the fix in
// packetup_allocation_wait_test.go), so this uses a flow that is already past its opening
// and a data share that is full.
func TestPacketUpGrantRollsBackWhenTheArrayCannotBeReserved(t *testing.T) {
	t.Parallel()

	fixture := newAllocationWaitFixture(t)
	fixture.fillDataShare(t)

	// Push one flow past its opening (64 KiB accepted) so its next write is data-class.
	conn := dialBatchConn(t, fixture.client)
	if result := awaitPayloadWrite(t, writePayloadAsync(conn, make([]byte, 64*1024)), 64*1024); result.err != nil || result.n != 64*1024 {
		t.Fatalf("opening Write = (%d, %v)", result.n, result.err)
	}
	before := fixture.pool.stats()

	blocked := writePayloadAsync(conn, bytes.Repeat([]byte{'s'}, 8*1024))
	waitForPool(t, fixture.pool, "the data-class writer waiting for array capacity", func(stats uplinkBudgetStats) bool {
		return stats.AllocWaiters == 1
	})
	after := fixture.pool.stats()
	if after.Total() != before.Total() {
		t.Fatalf("a rolled-back admission is still charged: total %d -> %d (%+v)", before.Total(), after.Total(), after)
	}
	if after.Allocated != before.Allocated {
		t.Fatalf("a failed reservation left allocation behind: %d -> %d", before.Allocated, after.Allocated)
	}
	select {
	case result := <-blocked:
		t.Fatalf("the write returned instead of waiting for its array: (%d, %v)", result.n, result.err)
	default:
	}
	// Quiet while nothing changes.
	if churn := grantChurn(fixture.pool, 40*time.Millisecond); churn > 512*1024 {
		t.Fatalf("a parked data writer churned %d granted bytes in 40 ms", churn)
	}

	// A real release lets the blocked write finish with its own admission.
	close(fixture.gate)
	if result := awaitPayloadWrite(t, blocked, 8*1024); result.err != nil || result.n != 8*1024 {
		t.Fatalf("blocked Write = (%d, %v), want (8192, nil)", result.n, result.err)
	}
	waitForPool(t, fixture.pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
	final := fixture.pool.stats()
	if final.AllocGrantedBytes != final.AllocReleasedBytes || final.GrantedBytes != final.ReleasedBytes {
		t.Fatalf("ledgers do not close: %+v", final)
	}
}

// TestPacketUpReplacementOverlapIsBounded grows one stream repeatedly while a POST owns
// the old array and asserts the documented bounds: settled allocation never exceeds
// AllocCapacity, the peak (which includes the copied-from array) never exceeds twice
// that, and the overlap returns to zero.
func TestPacketUpReplacementOverlapIsBounded(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	transport.responseDelay = 100 * time.Microsecond
	client := budgetedBatchClientWith(t, transport, metaOptions{ScMaxEachPostBytes: "8192-8192"}, 256*1024, 32*1024)
	pool := client.budget
	conn := dialBatchConn(t, client)
	sizes := []int{2048, 4096, 8192, 16 * 1024, 32 * 1024, 64 * 1024, 96 * 1024}
	for round, size := range sizes {
		if result := awaitPayloadWrite(t, writePayloadAsync(conn, bytes.Repeat([]byte{byte(round)}, size)), size); result.err != nil || result.n != size {
			t.Fatalf("round %d Write = (%d, %v)", round, result.n, result.err)
		}
		stats := pool.stats()
		if stats.Allocated > stats.AllocCapacity {
			t.Fatalf("round %d: settled allocation %d exceeds the bound %d", round, stats.Allocated, stats.AllocCapacity)
		}
		if stats.AllocPeak > 2*stats.AllocCapacity {
			t.Fatalf("round %d: overlap peak %d exceeds twice the bound %d", round, stats.AllocPeak, 2*stats.AllocCapacity)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0 && stats.AllocOverlap == 0
	})
	final := pool.stats()
	if final.AllocPeak > 2*final.AllocCapacity {
		t.Fatalf("final overlap peak %d exceeds twice the bound %d", final.AllocPeak, final.AllocCapacity)
	}
	if final.AllocGrantedBytes != final.AllocReleasedBytes || final.LeftoverAllocBytes != 0 {
		t.Fatalf("allocation ledger does not close: %+v", final)
	}
}

// TestPacketUpTightAllocationLedgerKeepsProgress is the -race and liveness target for the
// grant/rollback retry path: several streams write through a ledger far tighter than
// production, so admissions and reservations constantly succeed one and fail the other.
// The test asserts progress (every byte is accepted), the bounds, and a closed ledger.
func TestPacketUpTightAllocationLedgerKeepsProgress(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	transport.responseDelay = 50 * time.Microsecond
	client := budgetedBatchClientWith(t, transport, metaOptions{ScMaxEachPostBytes: "8192-8192"}, 32*1024, 8*1024)
	pool := client.budget
	setAllocationLimits(pool, 48*1024, 16*1024)

	const streams = 5
	conns := make([]net.Conn, 0, streams)
	for index := 0; index < streams; index++ {
		conns = append(conns, dialBatchConn(t, client))
	}
	var waitGroup sync.WaitGroup
	random := rand.New(rand.NewSource(7))
	var randomAccess sync.Mutex
	for index, conn := range conns {
		waitGroup.Add(1)
		go func(conn net.Conn, seed int) {
			defer waitGroup.Done()
			for round := 0; round < 12; round++ {
				randomAccess.Lock()
				size := 1 + random.Intn(24*1024)
				randomAccess.Unlock()
				if _, err := conn.Write(bytes.Repeat([]byte{byte('a' + seed%26)}, size)); err != nil {
					t.Errorf("stream %d Write: %v", seed, err)
					return
				}
			}
		}(conn, index)
	}
	done := make(chan struct{})
	go func() { waitGroup.Wait(); close(done) }()
	deadline := time.Now().Add(30 * time.Second)
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			stats := pool.stats()
			if stats.Allocated > stats.AllocCapacity {
				t.Fatalf("allocated %d exceeds the bound %d under contention", stats.Allocated, stats.AllocCapacity)
			}
			if stats.AllocPeak > 2*stats.AllocCapacity {
				t.Fatalf("overlap peak %d exceeds twice the bound %d", stats.AllocPeak, 2*stats.AllocCapacity)
			}
			if time.Now().After(deadline) {
				t.Fatalf("writers made no progress under the tight ledger: %+v", stats)
			}
			time.Sleep(50 * time.Microsecond)
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
	if final.ExcessReleases != 0 || final.LeftoverAllocBytes != 0 {
		t.Fatalf("ledger defects: %+v", final)
	}
	if final.AllocGrantedBytes != final.AllocReleasedBytes || final.GrantedBytes != final.ReleasedBytes {
		t.Fatalf("ledgers do not close: %+v", final)
	}
}
