package v2rayxhttp

import (
	"bytes"
	"net/http"
	"sync"
	"testing"
	"time"
)

// The allocation-wait behaviour of the write path.
//
// The audit's scenario: 226 one-byte streams each hold a 4 KiB queue array (their POSTs
// are gated), which fills the DATA share of the allocation ledger exactly (925,696 B at
// ScMaxEachPostBytes=32768). The main logical pool is nearly empty and the control
// allocation reserve is free. A new stream's 1 KiB opening used to be granted main
// LOGICAL quota, refused the last DATA capacity, and then spin: the rollback broadcast
// to all waiters closed the waiter's own captured wake channel, so it retried
// immediately - about 150k rounds / 150 MB of grant-and-return in 40 ms, with no POST
// ever reaching the transport.
//
// The fix has two halves, and these tests pin both:
//
//   - capacity choice: a write that is eligible for the opening pools reserves its array
//     as an OPENING (the whole allocation bound, not only the data share), even when the
//     main pool supplied the logical bytes;
//   - no spin: a rollback does not broadcast (the logical totals are unchanged), and the
//     main-waiter yield never wakes the yielding waiter. A writer therefore parks until
//     a REAL event - a release, a Close - as the bounded-churn assertions show.

// allocationWaitFixture builds the production budget for 32 KiB posts with every POST
// gated, and returns a helper that fills the data share of the allocation ledger with
// one-byte streams.
type allocationWaitFixture struct {
	transport *probeTransport
	gate      chan struct{}
	once      sync.Once
	pool      *sharedUplinkBudget
	client    *Client
}

func newAllocationWaitFixture(t *testing.T) *allocationWaitFixture {
	t.Helper()
	fixture := &allocationWaitFixture{
		transport: newProbeTransport(),
		gate:      make(chan struct{}),
	}
	fixture.transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		<-fixture.gate
		return okProbeResponse(), nil
	}
	fixture.client = budgetedBatchClient(t, fixture.transport, metaOptions{ScMaxEachPostBytes: "32768"})
	fixture.pool = fixture.client.budget
	stats := fixture.pool.stats()
	if stats.Capacity != 256*1024 || stats.Control != 128*1024 || stats.AllocDataLimit != 925696 {
		t.Fatalf("unexpected production sizing: %+v", stats)
	}
	return fixture
}

// fillDataShare opens one-byte streams until the data allocation share is exactly full.
// Each write gets main logical quota (the main pool is nearly empty) and a 4 KiB array,
// so the data share is consumed 4 KiB at a time.
func (f *allocationWaitFixture) fillDataShare(t *testing.T) {
	t.Helper()
	want := f.pool.stats().AllocDataLimit / packetUpAllocGranule
	for index := 0; index < want; index++ {
		conn := dialBatchConn(t, f.client)
		if result := awaitPayloadWrite(t, writePayloadAsync(conn, []byte{0x01}), 1); result.err != nil || result.n != 1 {
			t.Fatalf("filler %d Write = (%d, %v)", index, result.n, result.err)
		}
	}
	waitForPool(t, f.pool, "data share full", func(stats uplinkBudgetStats) bool {
		return stats.Allocated == stats.AllocDataLimit
	})
}

// grantChurn samples how many logical bytes the pool granted during a short window while
// nothing changes. A parked writer must show a small constant; a spinning one shows
// megabytes.
func grantChurn(pool *sharedUplinkBudget, window time.Duration) uint64 {
	before := pool.stats().GrantedBytes
	time.Sleep(window)
	after := pool.stats().GrantedBytes
	return after - before
}

// TestPacketUpOpeningGetsItsArrayWhenTheDataLedgerIsFull is the audit reproduction's
// first half: with the data share full, a new stream's 1 KiB opening must still get its
// array from the opening reserve and reach the transport.
func TestPacketUpOpeningGetsItsArrayWhenTheDataLedgerIsFull(t *testing.T) {
	t.Parallel()

	fixture := newAllocationWaitFixture(t)
	defer close(fixture.gate)
	fixture.fillDataShare(t)
	before := fixture.pool.stats()
	if before.ControlUsed != 0 {
		t.Fatalf("the one-byte fillers charged the control pool: %+v", before)
	}

	opener := dialBatchConn(t, fixture.client)
	opening := bytes.Repeat([]byte{'o'}, 1024)
	done := writePayloadAsync(opener, opening)
	select {
	case result := <-done:
		if result.err != nil || result.n != len(opening) {
			t.Fatalf("opening Write = (%d, %v), want (%d, nil)", result.n, result.err, len(opening))
		}
	case <-time.After(2 * time.Second):
		last := fixture.pool.stats()
		t.Fatalf("the 1 KiB opening never returned: Allocated=%d/%d ControlUsed=%d/%d "+
			"granted churn over 40ms=%d bytes, AllocWaiters=%d",
			last.Allocated, last.AllocCapacity, last.ControlUsed, last.Control,
			grantChurn(fixture.pool, 40*time.Millisecond), last.AllocWaiters)
	}
	waitForSessionUpload(t, fixture.transport, opener.(*packetConn).sessionID, 1)
	admitted := fixture.pool.stats()
	if admitted.Allocated <= admitted.AllocDataLimit {
		t.Fatalf("the opening did not use the control allocation reserve: %+v", admitted)
	}
	if admitted.Allocated > admitted.AllocCapacity {
		t.Fatalf("allocated %d exceeds the bound %d", admitted.Allocated, admitted.AllocCapacity)
	}
	// The main pool supplied the logical bytes (that is the main-first rule), so the
	// control LOGICAL pool may stay at zero; what matters is that the array came from the
	// protected share, which the assertion above proves.
	if admitted.Used > 64*1024 {
		t.Fatalf("the opening's logical bytes were not admitted from the roomy main pool: %+v", admitted)
	}
}

// TestPacketUpDataWriteStopsWhenTheDataLedgerIsFull is the second half: a write that is
// NOT opening-eligible gets no control reserve, so with the data share full it must park
// - and park quietly, not spin.
func TestPacketUpDataWriteStopsWhenTheDataLedgerIsFull(t *testing.T) {
	t.Parallel()

	fixture := newAllocationWaitFixture(t)
	defer close(fixture.gate)
	fixture.fillDataShare(t)

	blocked := dialBatchConn(t, fixture.client)
	// Larger than the short class: a bulk write, only the main pool may admit it, and its
	// array comes from the data share.
	result := writePayloadAsync(blocked, bytes.Repeat([]byte{'d'}, 300*1024))
	select {
	case outcome := <-result:
		t.Fatalf("a bulk write returned although the data share is full: (%d, %v)", outcome.n, outcome.err)
	case <-time.After(100 * time.Millisecond):
	}
	churn := grantChurn(fixture.pool, 40*time.Millisecond)
	if churn > 512*1024 {
		t.Fatalf("a parked bulk writer churned %d granted bytes in 40 ms (about %d rounds): "+
			"a rollback must not wake the writer it rolled back",
			churn, churn/uint64(300*1024))
	}
	select {
	case outcome := <-result:
		t.Fatalf("the bulk write returned during the observation window: (%d, %v)", outcome.n, outcome.err)
	default:
	}
	if churn > 0 {
		// One grant-and-return for the refused attempt is the expected constant.
		t.Logf("bulk writer churn over 40ms: %d bytes", churn)
	}
}

// TestPacketUpOpeningWaitsBoundedlyWhenAllAllocationIsFull is the "control capacity is
// also full" case: protected openings fill the rest of the ledger, so the next opening
// has nowhere to go. It must stop (bounded churn) and proceed once a POST returns.
func TestPacketUpOpeningWaitsBoundedlyWhenAllAllocationIsFull(t *testing.T) {
	t.Parallel()

	fixture := newAllocationWaitFixture(t)
	gate := fixture.gate
	fixture.fillDataShare(t)

	// Fill the remaining allocation bound with protected (opening) arrays, 4 KiB each.
	remaining := fixture.pool.stats().AllocCapacity - fixture.pool.stats().Allocated
	openings := remaining / packetUpAllocGranule
	for index := 0; index < openings; index++ {
		conn := dialBatchConn(t, fixture.client)
		if result := awaitPayloadWrite(t, writePayloadAsync(conn, []byte{0x02}), 1); result.err != nil || result.n != 1 {
			t.Fatalf("protected filler %d Write = (%d, %v)", index, result.n, result.err)
		}
	}
	waitForPool(t, fixture.pool, "allocation bound full", func(stats uplinkBudgetStats) bool {
		return stats.Allocated == stats.AllocCapacity
	})

	// The next opening must wait, quietly.
	waiter := dialBatchConn(t, fixture.client)
	blocked := writePayloadAsync(waiter, bytes.Repeat([]byte{'w'}, 1024))
	select {
	case result := <-blocked:
		t.Fatalf("an opening returned although the allocation bound is full: (%d, %v)", result.n, result.err)
	case <-time.After(100 * time.Millisecond):
	}
	churn := grantChurn(fixture.pool, 40*time.Millisecond)
	if churn > 512*1024 {
		t.Fatalf("the parked opening churned %d granted bytes in 40 ms", churn)
	}
	waitForPool(t, fixture.pool, "the opening registered as an allocation waiter", func(stats uplinkBudgetStats) bool {
		return stats.AllocWaiters >= 1
	})

	// A real release (let the gated POSTs finish) must wake it.
	close(gate)
	select {
	case result := <-blocked:
		if result.err != nil || result.n != 1024 {
			t.Fatalf("the parked opening Write = (%d, %v), want (1024, nil) after a release", result.n, result.err)
		}
	case <-time.After(writeFreeBudget):
		t.Fatal("a real capacity release never woke the parked opening")
	}
	waitForSessionUpload(t, fixture.transport, waiter.(*packetConn).sessionID, 1)
	waitForPool(t, fixture.pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
}
