package v2rayxhttp

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

// The shared pool's settlement boundaries, one behaviour per test: the per-stream
// bound still binds with a pool attached, a write interrupted by Close reports the
// bytes it actually accepted and settles exactly those, and a charge that outlives
// Close is returned when the POST that owns it finally returns.
//
// These are the cases where "the pool returns to baseline" could be true on paper
// and false in fact: a partial write that forgot its charge, or a Close that
// released bytes a wedged POST still owns, would look correct in a drained
// snapshot and be wrong in the window a diagnosis actually sees.

// TestPacketUpSharedBudgetKeepsThePerStreamBound proves the two bounds are
// independent: a pool with room to spare does not let one stream queue more than
// its own maxPending.
func TestPacketUpSharedBudgetKeepsThePerStreamBound(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	gate := make(chan struct{})
	transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		<-gate
		return okProbeResponse(), nil
	}
	client := budgetedBatchClient(t, transport, metaOptions{ScMaxEachPostBytes: "30000-30000"})
	pool := client.budget
	conn := dialBatchConn(t, client)
	packet := conn.(*packetConn)
	bound := packet.uplink.maxPending
	if pool.stats().Capacity <= bound {
		t.Fatalf("pool capacity %d must exceed the %d-byte per-stream bound for this test to mean anything",
			pool.stats().Capacity, bound)
	}

	write := writePayloadAsync(conn, bytes.Repeat([]byte("s"), bound*3))
	// The stream's ceiling is its queue plus the one batch a POST may own: the
	// pool adds no headroom of its own.
	streamCeiling := bound + client.meta.scMaxEachPostBytes.max
	deadline := time.Now().Add(writeFreeBudget)
	for {
		stats := packet.uplink.stats()
		if stats.PendingBytes+stats.InFlightBytes > streamCeiling {
			t.Fatalf("one stream retained %d bytes, above its own ceiling %d (pending %d + in-flight %d)",
				stats.PendingBytes+stats.InFlightBytes, streamCeiling, stats.PendingBytes, stats.InFlightBytes)
		}
		if stats.PendingBytes+stats.InFlightBytes >= bound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the queue never filled to its bound: %+v", stats)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case result := <-write:
		t.Fatalf("a three-bound Write returned while the queue was full: (%d, %v)", result.n, result.err)
	case <-time.After(50 * time.Millisecond):
	}
	close(gate)
	if result := awaitPayloadWrite(t, write, bound*3); result.err != nil || result.n != bound*3 {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", result.n, result.err, bound*3)
	}
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0
	})
}

// TestPacketUpSharedBudgetSettlesAPartialWriteOnClose interrupts a write with
// Close: the call must return the byte count it actually accepted, those accepted
// bytes must be the stream's whole charge, and closing must not invent or lose any
// of it.
func TestPacketUpSharedBudgetSettlesAPartialWriteOnClose(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	gate := make(chan struct{})
	transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		<-gate
		return okProbeResponse(), nil
	}
	client := budgetedBatchClientWith(t, transport, metaOptions{ScMaxEachPostBytes: "30000-30000"}, 128*1024, 16*1024)
	pool := client.budget
	conn := dialBatchConn(t, client)
	packet := conn.(*packetConn)
	bound := packet.uplink.maxPending

	write := writePayloadAsync(conn, bytes.Repeat([]byte("p"), bound*2))
	deadline := time.Now().Add(writeFreeBudget)
	for {
		stats := packet.uplink.stats()
		if stats.PendingBytes+stats.InFlightBytes >= bound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the queue never filled: %+v", stats)
		}
		time.Sleep(time.Millisecond)
	}

	result := awaitPayloadWrite(t, closeReturningWrite(t, conn, write), 0)
	if result.err == nil {
		t.Fatalf("a write interrupted by Close reported success: n=%d", result.n)
	}
	stats := packet.uplink.stats()
	if result.n != stats.PendingBytes+stats.InFlightBytes {
		t.Fatalf("Write reported %d accepted bytes but the stream retains %d (pending %d + in-flight %d)",
			result.n, stats.PendingBytes+stats.InFlightBytes, stats.PendingBytes, stats.InFlightBytes)
	}
	if stats.AcceptedBytes != uint64(result.n) {
		t.Fatalf("accepted counter %d, but the caller was told %d", stats.AcceptedBytes, result.n)
	}

	// The accepted bytes are still charged: the posts that own them have not
	// returned.
	if held := pool.stats().Total(); held != result.n {
		t.Fatalf("pool holds %d bytes, want the %d accepted", held, result.n)
	}
	close(gate)
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0
	})
	requireUplinkLedger(t, packet.uplink.stats())
}

// closeReturningWrite runs Close concurrently with a blocked Write and returns the
// write's outcome: Close is what unblocks it, so the two must overlap.
func closeReturningWrite(t *testing.T, conn interface{ Close() error }, write <-chan writeResult) <-chan writeResult {
	t.Helper()
	out := make(chan writeResult, 1)
	go func() {
		result := <-write
		out <- result
	}()
	if err := conn.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	return out
}

// TestPacketUpSharedBudgetReturnsAChargeThatOutlivesClose pins the late settlement.
// Close gives the queue a bounded drain (packetUpDrainTimeout); a POST wedged past
// that keeps owning its bytes, so the charge must stay until the POST returns and
// the pool must return to baseline then - not when Close returned.
func TestPacketUpSharedBudgetReturnsAChargeThatOutlivesClose(t *testing.T) {
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
		t.Fatalf("Write = (%d, %v), want (%d, nil)", result.n, result.err, len(payload))
	}
	select {
	case <-entered:
	case <-time.After(writeFreeBudget):
		t.Fatal("the post never started")
	}

	started := time.Now()
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(started); elapsed < packetUpDrainTimeout {
		t.Fatalf("Close returned after %s, before its %s drain budget: the gated post should have held it", elapsed, packetUpDrainTimeout)
	}
	// The wedge outlived Close, so the bytes - and their charge - are still there.
	held := pool.stats()
	if held.Total() != len(payload) {
		t.Fatalf("pool holds %d bytes after a timed-out Close, want the %d the post still owns",
			held.Total(), len(payload))
	}

	close(gate)
	waitForPool(t, pool, "late settlement", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0
	})
	final := pool.stats()
	if final.GrantedBytes != final.ReleasedBytes || final.ExcessReleases != 0 || final.LeftoverBytes != 0 {
		t.Fatalf("late settlement did not close the pool's flow: %+v", final)
	}
}
