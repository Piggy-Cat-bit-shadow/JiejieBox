package v2rayxhttp

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// The control headroom, from the real dial and packet-up write path.
//
// The audit reproduced two failures of the first shared-pool design (see
// uplink_budget.go) that these tests pin:
//
//	P1-A.1 a COLD-START LONG flow's opening writes drain the reserve, so the next
//	       stream's first packet waits behind bulk data;
//	P1-A.2 one oversized opening write takes the whole reserve, with the same
//	       result.
//
// Both are the same defect: the reserve was asked to be two things at once — the
// handshake/control path AND the short-flow data path — so data could spend the
// guarantee. The fix separates them: a dedicated control pool, which only a flow's
// opening may draw on, with a per-flow ceiling, so a single opening (one write or
// several) cannot consume the guarantee the next opening needs.
//
// The tests observe the guarantee where it matters: the new stream's POST reaches
// the RoundTripper while the bulk POSTs are still gated, i.e. before any of them
// returns or is released.

// budgetedBatchClientWithControl is budgetedBatchClientWith with an explicit
// control pool, for the tests that fill it on purpose.
func budgetedBatchClientWithControl(t *testing.T, transport http.RoundTripper, options metaOptions, capacity, reserve, control int) *Client {
	t.Helper()
	client := batchClient(t, transport, options)
	client.budget = newSharedUplinkBudgetWithControl(capacity, reserve, control)
	return client
}

// waitForSessionUpload waits until the transport has recorded `want` upload POSTs
// for one session. Posts are recorded before onPost runs, so a gated post is
// visible here: that is what makes "the opening reached the wire before the bulk
// gate was released" observable rather than inferred.
func waitForSessionUpload(t *testing.T, transport *probeTransport, sessionID string, want int) {
	t.Helper()
	deadline := time.Now().Add(writeFreeBudget)
	for {
		_, paths, _, _ := transport.snapshot()
		count := 0
		for _, requestPath := range paths {
			if strings.Contains(requestPath, sessionID) {
				count++
			}
		}
		if count >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s reached %d upload posts, want %d (the opening never left the queue)", sessionID, count, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// gatedPosts is the shared fixture: every upload POST is held inside onPost, so no
// charge is released while a test is making its point.
func gatedPosts() (*probeTransport, chan struct{}) {
	transport := newProbeTransport()
	gate := make(chan struct{})
	transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		<-gate
		return okProbeResponse(), nil
	}
	return transport, gate
}

// poolTotal is the aggregate the pool reports, the number every "drained" check
// and every bound assertion must use now that there are three pools.
func poolTotal(stats uplinkBudgetStats) int {
	return stats.Used + stats.ReservedUsed + stats.ControlUsed
}

// TestPacketUpOpeningSurvivesAColdStartLongFlow is audit P1-A.1.
//
// At the header: main pool 64 KiB, reserve 32 KiB, every data POST gated. Stream A
// fills the main pool. Stream B — cold, so it still looks like an opening by its
// write sizes — writes four 8 KiB blocks and takes the whole reserve. Stream C then
// writes a 1 KiB first packet and waits forever: main full, reserve full.
//
// With the control pool the same workload must admit C: B may spend its per-flow
// control ceiling (packetUpStreamControl) and then its data reserve, and C's opening
// still has control headroom because data cannot spend it.
func TestPacketUpOpeningSurvivesAColdStartLongFlow(t *testing.T) {
	t.Parallel()

	transport, gate := gatedPosts()
	const (
		capacity = 64 * 1024
		reserve  = 32 * 1024
	)
	client := budgetedBatchClientWithControl(t, transport, metaOptions{}, capacity, reserve, packetUpControlBytes)
	pool := client.budget
	bulk := dialBatchConn(t, client)

	// A fills the main pool.
	if result := awaitPayloadWrite(t, writePayloadAsync(bulk, make([]byte, capacity)), capacity); result.err != nil || result.n != capacity {
		t.Fatalf("bulk main-pool fill Write = (%d, %v), want (%d, nil)", result.n, result.err, capacity)
	}
	waitForPool(t, pool, "main pool full", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})

	// B is a cold-start long flow: four 8 KiB blocks is how a relay reads it. It
	// must not be able to spend the whole control guarantee, whatever it looks like
	// from one write at a time.
	cold := dialBatchConn(t, client)
	chunk := make([]byte, 8*1024)
	for index := 0; index < 4; index++ {
		if result := awaitPayloadWrite(t, writePayloadAsync(cold, chunk), len(chunk)); result.err != nil || result.n != len(chunk) {
			t.Fatalf("cold-start block %d Write = (%d, %v), want (%d, nil)", index, result.n, result.err, len(chunk))
		}
	}
	hold := pool.stats()
	if hold.ControlUsed > packetUpStreamControl {
		t.Fatalf("one cold-start flow holds %d control bytes, over the %d-byte per-flow ceiling",
			hold.ControlUsed, packetUpStreamControl)
	}
	if hold.Used != capacity {
		t.Fatalf("main pool %d, want it still full at %d", hold.Used, capacity)
	}

	// C is a new stream: its 1 KiB first packet has to be accepted AND posted while
	// every bulk POST is still gated.
	opener := dialBatchConn(t, client)
	opening := bytes.Repeat([]byte("o"), 1024)
	if result := awaitPayloadWrite(t, writePayloadAsync(opener, opening), len(opening)); result.err != nil || result.n != len(opening) {
		t.Fatalf("new stream opening Write = (%d, %v), want (%d, nil)", result.n, result.err, len(opening))
	}
	waitForSessionUpload(t, transport, opener.(*packetConn).sessionID, 1)

	admitted := pool.stats()
	if admitted.ControlUsed <= 0 {
		t.Fatalf("the opening was admitted without charging the control pool: %+v", admitted)
	}
	if admitted.Used != capacity {
		t.Fatalf("main pool %d after the opening, want it still full at %d (the opening must not depend on main room)",
			admitted.Used, capacity)
	}
	if total := poolTotal(admitted); total > admitted.Capacity+admitted.Reserve+admitted.Control {
		t.Fatalf("aggregate %d exceeds capacity+reserve+control %d",
			total, admitted.Capacity+admitted.Reserve+admitted.Control)
	}
	if admitted.Peak > admitted.Capacity+admitted.Reserve+admitted.Control {
		t.Fatalf("peak %d exceeds capacity+reserve+control %d",
			admitted.Peak, admitted.Capacity+admitted.Reserve+admitted.Control)
	}

	// Everything released: the pool must return to baseline with no defect counters.
	close(gate)
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return poolTotal(stats) == 0
	})
	for _, conn := range []net.Conn{bulk, cold, opener} {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	final := waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if final.ExcessReleases != 0 || final.LeftoverBytes != 0 {
		t.Fatalf("pool accounting defects: %+v", final)
	}
	if final.GrantedBytes != final.ReleasedBytes {
		t.Fatalf("granted %d != released %d: a charge was never returned", final.GrantedBytes, final.ReleasedBytes)
	}
}

// TestPacketUpOversizedOpeningWriteCannotSpendTheControlHeadroom is audit P1-A.2.
//
// At the header: main pool 512 KiB, reserve 256 KiB, main full, then a second new
// stream writes 256 KiB in ONE call. The old grant rule looked at acceptedBytes only
// once, before the write, so that call took the entire reserve and the next stream's
// 1 KiB opening waited. Now the control pool is the opening guarantee: the 256 KiB
// write may still use the data reserve (that is what the reserve is for), but its
// control draw is bounded by packetUpStreamControl, so the next opener is admitted.
//
// It also pins the other half of the rule: a write outside the short class cannot
// touch the control pool at all, which is why the 512 KiB main-pool fill takes none.
func TestPacketUpOversizedOpeningWriteCannotSpendTheControlHeadroom(t *testing.T) {
	t.Parallel()

	transport, gate := gatedPosts()
	const (
		capacity = 512 * 1024
		reserve  = 256 * 1024
	)
	client := budgetedBatchClientWithControl(t, transport, metaOptions{}, capacity, reserve, packetUpControlBytes)
	pool := client.budget
	bulk := dialBatchConn(t, client)

	// The main-pool fill is outside the short class (packetUpSmallWrite), so it must
	// not draw on the control guarantee.
	if result := awaitPayloadWrite(t, writePayloadAsync(bulk, make([]byte, capacity)), capacity); result.err != nil || result.n != capacity {
		t.Fatalf("bulk fill Write = (%d, %v), want (%d, nil)", result.n, result.err, capacity)
	}
	waitForPool(t, pool, "main pool full", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})
	if stats := pool.stats(); stats.ControlUsed != 0 {
		t.Fatalf("a %d-byte bulk write charged %d control bytes: only the short class may draw the guarantee",
			capacity, stats.ControlUsed)
	}

	// One 256 KiB opening write: short class, so the data reserve is fair game, but
	// its control draw is capped per flow.
	shortFlow := dialBatchConn(t, client)
	body := bytes.Repeat([]byte("s"), 256*1024)
	if result := awaitPayloadWrite(t, writePayloadAsync(shortFlow, body), len(body)); result.err != nil || result.n != len(body) {
		t.Fatalf("short-flow Write = (%d, %v), want (%d, nil)", result.n, result.err, len(body))
	}
	after := pool.stats()
	if after.ControlUsed > packetUpStreamControl {
		t.Fatalf("one short flow holds %d control bytes, over the %d-byte per-flow ceiling",
			after.ControlUsed, packetUpStreamControl)
	}
	if after.ReservedUsed == 0 {
		t.Fatalf("the short flow never reached the data reserve: %+v (the opening was not the test's shape)", after)
	}

	// The next new stream still gets its opening through the control pool.
	opener := dialBatchConn(t, client)
	opening := bytes.Repeat([]byte("n"), 1024)
	if result := awaitPayloadWrite(t, writePayloadAsync(opener, opening), len(opening)); result.err != nil || result.n != len(opening) {
		t.Fatalf("new stream opening Write = (%d, %v), want (%d, nil)", result.n, result.err, len(opening))
	}
	waitForSessionUpload(t, transport, opener.(*packetConn).sessionID, 1)
	if stats := pool.stats(); stats.Used != capacity {
		t.Fatalf("main pool %d after the opening, want it still full at %d", stats.Used, capacity)
	}

	close(gate)
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return poolTotal(stats) == 0
	})
	for _, conn := range []net.Conn{bulk, shortFlow, opener} {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	final := waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if final.ExcessReleases != 0 || final.LeftoverBytes != 0 || final.GrantedBytes != final.ReleasedBytes {
		t.Fatalf("pool flow does not close: %+v", final)
	}
}

// TestPacketUpControlHeadroomFullWaitsAndCancels pins the bounded-wait half of the
// rule: the control guarantee is a bound, not an unlimited promise. When it is full
// (and the main pool with it) a new opening waits, exactly like any other charge —
// and it is released by a post returning OR by the conn closing, never by spinning.
func TestPacketUpControlHeadroomFullWaitsAndCancels(t *testing.T) {
	t.Parallel()

	transport, gate := gatedPosts()
	const (
		// The control pool is clamped to half the main capacity, so the main pool has
		// to be at least twice the guarantee for this configuration to be its own.
		capacity = 32 * 1024
		control  = 16 * 1024
	)
	client := budgetedBatchClientWithControl(t, transport, metaOptions{}, capacity, 0, control)
	pool := client.budget
	filler := dialBatchConn(t, client)

	// Fill the main pool, then the control pool, from one flow: the per-flow ceiling
	// equals the whole control pool in this deliberately tiny configuration.
	if result := awaitPayloadWrite(t, writePayloadAsync(filler, make([]byte, capacity)), capacity); result.err != nil || result.n != capacity {
		t.Fatalf("main fill Write = (%d, %v)", result.n, result.err)
	}
	waitForPool(t, pool, "main full", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})
	if result := awaitPayloadWrite(t, writePayloadAsync(filler, make([]byte, control)), control); result.err != nil || result.n != control {
		t.Fatalf("control fill Write = (%d, %v)", result.n, result.err)
	}
	waitForPool(t, pool, "control full", func(stats uplinkBudgetStats) bool {
		return stats.ControlUsed == stats.Control && stats.Used == stats.Capacity
	})

	// A new opening has nowhere to go: it must wait, not fail and not be admitted by
	// overdrawing either pool.
	waiter := dialBatchConn(t, client)
	blocked := writePayloadAsync(waiter, make([]byte, 1024))
	waitForPool(t, pool, "one queued opening", func(stats uplinkBudgetStats) bool {
		return stats.Waiters == 1
	})
	select {
	case result := <-blocked:
		t.Fatalf("the opening Write returned while main and control were full: (%d, %v)", result.n, result.err)
	default:
	}

	// Cancellation: Close must release the waiter promptly, with ErrClosed.
	closed := make(chan error, 1)
	go func() { closed <- waiter.Close() }()
	select {
	case result := <-blocked:
		if !errors.Is(result.err, net.ErrClosed) {
			t.Fatalf("a cancelled opening Write reported (%d, %v), want net.ErrClosed", result.n, result.err)
		}
	case <-time.After(writeFreeBudget):
		t.Fatal("Close did not release a write waiting on the control pool")
	}
	if err := <-closed; err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Bounded wait: a second opening waits until a post returns, then proceeds.
	next := dialBatchConn(t, client)
	pending := writePayloadAsync(next, make([]byte, 1024))
	waitForPool(t, pool, "the second opening queued", func(stats uplinkBudgetStats) bool {
		return stats.Waiters >= 1
	})
	close(gate)
	select {
	case result := <-pending:
		if result.err != nil || result.n != 1024 {
			t.Fatalf("the second opening Write = (%d, %v), want (1024, nil) once a post returned", result.n, result.err)
		}
	case <-time.After(writeFreeBudget):
		t.Fatal("releasing a post never woke the opening waiting on the control pool")
	}
	if err := next.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := filler.Close(); err != nil {
		t.Fatalf("Close filler: %v", err)
	}
	waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0 && poolTotal(stats) == 0
	})
}

// TestPacketUpControlHeadroomIsPartOfTheAggregate reconciles the three pools, so a
// future change cannot add one without extending the bound and the conservation
// identities. It is a cheap snapshot test, not a workload.
func TestPacketUpControlHeadroomIsPartOfTheAggregate(t *testing.T) {
	t.Parallel()

	transport, gate := gatedPosts()
	defer close(gate)
	client := budgetedBatchClientWith(t, transport, metaOptions{}, 64*1024, 16*1024)
	pool := client.budget
	conn := dialBatchConn(t, client)
	if _, err := conn.Write(bytes.Repeat([]byte("a"), 8*1024)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	waitForPool(t, pool, "charged", func(stats uplinkBudgetStats) bool {
		return poolTotal(stats) == 8*1024
	})
	stats := pool.stats()
	if stats.Control <= 0 {
		t.Fatalf("a pool built by production sizes has no control headroom: %+v", stats)
	}
	if stats.Control > stats.Capacity/2 {
		t.Fatalf("control pool %d exceeds half the main capacity %d: the guarantee may not outgrow the pool it protects",
			stats.Control, stats.Capacity)
	}
	held := conn.(*packetConn).uplink.stats().SharedHeldBytes
	if held != poolTotal(stats) {
		t.Fatalf("stream holds %d bytes, the pool reports %d", held, poolTotal(stats))
	}
	if got := stats.GrantedBytes - stats.ReleasedBytes; got != uint64(poolTotal(stats)) {
		t.Fatalf("granted-released = %d, want the aggregate %d", got, poolTotal(stats))
	}
}

// TestPacketUpControlHeadroomDefaultsAreThePolicy pins the constants the production
// constructor derives, so the three pools cannot drift apart silently.
func TestPacketUpControlHeadroomDefaultsAreThePolicy(t *testing.T) {
	t.Parallel()

	// The per-flow ceiling is part of the opening budget, and the pool holds a fixed
	// number of openings, so both follow from packetUpReserveOpeningBytes.
	if packetUpStreamControl <= 0 || packetUpStreamControl >= packetUpReserveOpeningBytes {
		t.Fatalf("per-flow control ceiling %d must be a positive fraction of the %d-byte opening budget",
			packetUpStreamControl, packetUpReserveOpeningBytes)
	}
	if packetUpControlBytes != packetUpControlOpenings*packetUpStreamControl {
		t.Fatalf("control pool %d != %d openings x %d", packetUpControlBytes, packetUpControlOpenings, packetUpStreamControl)
	}
	if packetUpControlBytes < packetUpStreamControl {
		t.Fatalf("control pool %d is smaller than one flow's ceiling %d", packetUpControlBytes, packetUpStreamControl)
	}
	pool := newSharedUplinkBudget(8<<20, 2<<20)
	stats := pool.stats()
	if stats.Control != packetUpControlBytes {
		t.Fatalf("pool control = %d, want %d at the default main size", stats.Control, packetUpControlBytes)
	}
	if stats.Control > stats.Capacity/2 {
		t.Fatalf("control %d exceeds half the capacity %d", stats.Control, stats.Capacity)
	}
	// The reserve clamp still applies to the reserve alone.
	small := newSharedUplinkBudget(8*1024, 64*1024)
	if got := small.stats(); got.Reserve > got.Capacity/2 {
		t.Fatalf("reserve %d exceeds half the %d-byte capacity", got.Reserve, got.Capacity)
	}
}

// TestPacketUpShortFlowStillUsesTheReserve is the no-regression control for the fix:
// the data reserve keeps its job (a short flow's body moves while the main pool is
// full), and it is the control pool's arrival, not a narrower reserve, that fixed
// the opening. The stream wrote 128 KiB in one call, so it is short-class data.
func TestPacketUpShortFlowStillUsesTheReserve(t *testing.T) {
	t.Parallel()

	transport, gate := gatedPosts()
	const (
		// The reserve is clamped to half the main capacity, so the main pool here is
		// twice the reserve: the short flow's body has to fit the reserve for this
		// test to be about the reserve rather than about the clamp.
		capacity = 256 * 1024
		reserve  = 128 * 1024
	)
	client := budgetedBatchClientWithControl(t, transport, metaOptions{}, capacity, reserve, packetUpControlBytes)
	pool := client.budget
	bulk := dialBatchConn(t, client)
	if result := awaitPayloadWrite(t, writePayloadAsync(bulk, make([]byte, capacity)), capacity); result.err != nil || result.n != capacity {
		t.Fatalf("main fill Write = (%d, %v)", result.n, result.err)
	}
	waitForPool(t, pool, "main full", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})
	shortFlow := dialBatchConn(t, client)
	body := make([]byte, 128*1024)
	if result := awaitPayloadWrite(t, writePayloadAsync(shortFlow, body), len(body)); result.err != nil || result.n != len(body) {
		t.Fatalf("short-flow Write = (%d, %v), want (%d, nil)", result.n, result.err, len(body))
	}
	stats := pool.stats()
	if stats.ReservedUsed == 0 {
		t.Fatalf("a 128 KiB short flow did not use the data reserve: %+v", stats)
	}
	if stats.ControlUsed > packetUpStreamControl {
		t.Fatalf("the short flow overdrew the control ceiling: %d > %d", stats.ControlUsed, packetUpStreamControl)
	}
	close(gate)
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return poolTotal(stats) == 0
	})
}

// TestPacketUpControlHeadroomIsSafeUnderConcurrency is the -race target for the
// third pool: streams that mix openings and bulk while the pool is too small to hold
// them, sampling the invariants from the write path.
func TestPacketUpControlHeadroomIsSafeUnderConcurrency(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	transport.responseDelay = 100 * time.Microsecond
	client := budgetedBatchClientWithControl(t, transport, metaOptions{}, 32*1024, 8*1024, 16*1024)
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
			for round := 0; round < 8; round++ {
				size := 1 + (seed*53+round*29)%4096
				if round%3 == 0 {
					size += 2 * 1024
				}
				if _, err := conn.Write(bytes.Repeat([]byte{'c'}, size)); err != nil {
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
			if stats.Used > stats.Capacity || stats.ReservedUsed > stats.Reserve || stats.ControlUsed > stats.Control {
				t.Fatalf("a pool exceeds its bound: %+v", stats)
			}
			if stats.Peak > stats.Capacity+stats.Reserve+stats.Control {
				t.Fatalf("peak %d exceeds the aggregate %d", stats.Peak, stats.Capacity+stats.Reserve+stats.Control)
			}
			if got := stats.GrantedBytes - stats.ReleasedBytes; got != uint64(poolTotal(stats)) {
				t.Fatalf("granted-released = %d, want aggregate %d", got, poolTotal(stats))
			}
			time.Sleep(100 * time.Microsecond)
		}
	}
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return poolTotal(stats) == 0
	})
	for _, conn := range conns {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	final := waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if final.ExcessReleases != 0 || final.LeftoverBytes != 0 || final.GrantedBytes != final.ReleasedBytes {
		t.Fatalf("pool flow does not close: %+v", final)
	}
}
