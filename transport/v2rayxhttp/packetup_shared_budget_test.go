package v2rayxhttp

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// The shared packet-up upload pool (uplink_budget.go), exercised from the real
// dial entry.
//
// What these tests are for, in the order the design claims it:
//
//   - the sum across streams is bounded, while the per-stream bound stays in
//     force and a saturated pool really does hold writers back (backpressure,
//     not a silent drop);
//   - the control path progresses: a small write may draw on the reserve while
//     bulk streams have exhausted the main pool, which is what "the control flow
//     can make progress" means in packet-up;
//   - a post failure settles the failed stream's charge without waiting for a
//     timeout, and the next stream waiting for capacity then proceeds;
//   - cancellation and close return the pool to its baseline, with the flow
//     counters proving nothing was released twice or left behind;
//   - the production constructor actually wires the pool, so these behaviours are
//     not a test-only island.

// budgetedBatchClient is batchClient with a shared pool built the way NewClient
// builds one: capacity derived from the same per-stream queue bound.
func budgetedBatchClient(t *testing.T, transport http.RoundTripper, options metaOptions) *Client {
	t.Helper()
	client := batchClient(t, transport, options)
	client.budget = newSharedUplinkBudget(packetUpSharedCapacity(packetUpPendingBound(client.meta)), packetUpSharedReserve)
	return client
}

// budgetedBatchClientWith is budgetedBatchClient with explicit pool sizes, for the
// tests that need a pool small enough to saturate in a few kilobytes.
func budgetedBatchClientWith(t *testing.T, transport http.RoundTripper, options metaOptions, capacity, reserve int) *Client {
	t.Helper()
	client := batchClient(t, transport, options)
	client.budget = newSharedUplinkBudget(capacity, reserve)
	return client
}

// waitForPool polls the pool until condition holds, failing with the last
// snapshot if it never does.
func waitForPool(t *testing.T, pool *sharedUplinkBudget, what string, condition func(uplinkBudgetStats) bool) uplinkBudgetStats {
	t.Helper()
	deadline := time.Now().Add(writeFreeBudget)
	for {
		stats := pool.stats()
		if condition(stats) {
			return stats
		}
		if time.Now().After(deadline) {
			t.Fatalf("pool never reached %q: %+v", what, stats)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func writePayloadAsync(conn net.Conn, payload []byte) <-chan writeResult {
	done := make(chan writeResult, 1)
	go func() {
		n, err := conn.Write(payload)
		done <- writeResult{n: n, err: err}
	}()
	return done
}

func awaitPayloadWrite(t *testing.T, done <-chan writeResult, want int) writeResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(writeFreeBudget):
		t.Fatalf("a %d-byte Write never returned: the pool or the queue never made room", want)
		return writeResult{}
	}
}

// streamCharges sums what every stream reports as held in the shared pool, and
// separately what each stream reports as pending+in-flight. While a stream is
// live these must agree, and their total must be what the pool says it holds.
func streamCharges(conns []net.Conn) (held int, retained int) {
	for _, conn := range conns {
		stats := conn.(*packetConn).uplink.stats()
		held += stats.SharedHeldBytes
		retained += stats.PendingBytes + stats.InFlightBytes
	}
	return held, retained
}

// TestPacketUpSharedBudgetBoundsTheStreamSum is the measurement this pool exists
// for. Six streams, each writing its own per-stream bound while every post is
// gated: before the pool the aggregate was the sum of all six (12,000,000 B with
// the default bound), and the assertion below failed with exactly that number.
func TestPacketUpSharedBudgetBoundsTheStreamSum(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	gate := make(chan struct{})
	posted := make(chan struct{})
	var postedOnce sync.Once
	transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		postedOnce.Do(func() { close(posted) })
		<-gate
		return okProbeResponse(), nil
	}
	client := budgetedBatchClient(t, transport, metaOptions{})
	pool := client.budget

	bound := packetUpPendingBound(client.meta)
	initial := pool.stats()
	if initial.Capacity != packetUpSharedCapacity(bound) {
		t.Fatalf("pool capacity %d, want %d for a per-stream bound of %d",
			initial.Capacity, packetUpSharedCapacity(bound), bound)
	}
	if initial.Capacity < bound {
		t.Fatalf("pool capacity %d is below one stream's bound %d: the pool may not be stricter than the stream", initial.Capacity, bound)
	}

	const streams = 6
	conns := make([]net.Conn, 0, streams)
	for index := 0; index < streams; index++ {
		conns = append(conns, dialBatchConn(t, client))
	}
	writes := make([]<-chan writeResult, streams)
	payloads := make([][]byte, streams)
	for index, conn := range conns {
		payloads[index] = bytes.Repeat([]byte{byte('a' + index)}, bound)
		writes[index] = writePayloadAsync(conn, payloads[index])
	}

	select {
	case <-posted:
	case <-time.After(writeFreeBudget):
		t.Fatal("no upload post started")
	}
	// Saturation is stable while every post is gated: no release can happen. Wait
	// for both halves of it — the pool full AND a stream queued behind it — so the
	// snapshot below is taken after the writers that must be held back have tried.
	saturated := waitForPool(t, pool, "saturated", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity && stats.Waiters > 0
	})
	held, retained := streamCharges(conns)
	if held != saturated.Used+saturated.ReservedUsed {
		t.Fatalf("streams hold %d shared bytes but the pool reports %d: one of them is not the sum of the other",
			held, saturated.Used+saturated.ReservedUsed)
	}
	if retained != held {
		t.Fatalf("pending+in-flight %d differs from the shared charge %d: the queue and its charge must be one thing",
			retained, held)
	}
	if saturated.Used > saturated.Capacity {
		t.Fatalf("main pool used %d exceeds its capacity %d", saturated.Used, saturated.Capacity)
	}
	if total := saturated.Used + saturated.ReservedUsed; total > saturated.Capacity+saturated.Reserve {
		t.Fatalf("aggregate retained %d exceeds capacity+reserve %d", total, saturated.Capacity+saturated.Reserve)
	}
	if saturated.Peak > saturated.Capacity+saturated.Reserve {
		t.Fatalf("peak %d exceeds capacity+reserve %d", saturated.Peak, saturated.Capacity+saturated.Reserve)
	}
	if saturated.ReservedUsed != 0 {
		t.Fatalf("bulk writes charged the reserve: %d bytes (the reserve is the control path)", saturated.ReservedUsed)
	}
	if saturated.Waiters == 0 {
		t.Fatal("no stream is queued for capacity: the writes did not contend")
	}
	blocked := 0
	completed := make([]*writeResult, streams)
	for index, done := range writes {
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatalf("a writer failed while the pool was gated: %v", result.err)
			}
			completed[index] = &result
		default:
			blocked++
		}
	}
	if blocked == 0 {
		t.Fatal("every writer finished while the pool was full: nothing was held back, so the bound is not backpressure")
	}

	close(gate)
	for index, done := range writes {
		if completed[index] != nil {
			continue
		}
		if result := awaitPayloadWrite(t, done, bound); result.err != nil || result.n != bound {
			t.Fatalf("stream %d Write = (%d, %v), want (%d, nil)", index, result.n, result.err, bound)
		}
	}
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Used+stats.ReservedUsed == 0
	})
	for _, conn := range conns {
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

// TestPacketUpSharedBudgetKeepsTheControlWriteMoving pins the reason the pool has
// a reserve at all: with the main pool exhausted by a bulk stream, a new stream's
// handshake write must still be accepted and posted, not parked until the bulk
// stream finishes.
func TestPacketUpSharedBudgetKeepsTheControlWriteMoving(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	gate := make(chan struct{})
	transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		<-gate
		return okProbeResponse(), nil
	}
	const (
		capacity = 32 * 1024
		reserve  = 8 * 1024
	)
	client := budgetedBatchClientWith(t, transport, metaOptions{}, capacity, reserve)
	pool := client.budget
	bulk := dialBatchConn(t, client)
	control := dialBatchConn(t, client)

	if result := awaitPayloadWrite(t, writePayloadAsync(bulk, make([]byte, capacity)), capacity); result.err != nil || result.n != capacity {
		t.Fatalf("bulk Write = (%d, %v), want (%d, nil)", result.n, result.err, capacity)
	}
	waitForPool(t, pool, "main pool full", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})

	handshake := make([]byte, 1024)
	write := writePayloadAsync(control, handshake)
	select {
	case result := <-write:
		if result.err != nil || result.n != len(handshake) {
			t.Fatalf("control Write = (%d, %v), want (%d, nil)", result.n, result.err, len(handshake))
		}
	case <-time.After(time.Second):
		t.Fatal("the control write never returned with the main pool full: the reserve is not admitting it")
	}
	// Progress means a POST, not merely a queued byte: the reserve must have been
	// charged and the flusher must have taken the payload.
	transport.waitForPosts(t, 2)
	stats := pool.stats()
	if stats.ReservedUsed != len(handshake) {
		t.Fatalf("reserve holds %d bytes, want the %d-byte control write", stats.ReservedUsed, len(handshake))
	}
	if stats.Used != stats.Capacity {
		t.Fatalf("main pool %d, want it still full at %d", stats.Used, stats.Capacity)
	}
	close(gate)
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Used+stats.ReservedUsed == 0
	})
}

// TestPacketUpSharedBudgetSettlesAFailedPostAgainstThePool pins the single
// settlement path: the bytes a failed post owned stop being charged in the same
// step they stop being counted as in-flight, without waiting for Close, and the
// stream that was queued behind them proceeds.
func TestPacketUpSharedBudgetSettlesAFailedPostAgainstThePool(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	gate := make(chan struct{})
	var once sync.Once
	refused := errors.New("upload refused")
	transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		var first bool
		once.Do(func() { first = true })
		if first {
			<-gate
			return nil, refused
		}
		return okProbeResponse(), nil
	}
	const (
		// The workload has to be BULK sized: since the reserve covers short writes up
		// to packetUpSmallWrite, a payload below that class would partly succeed from
		// the reserve instead of queueing, and this test is about a post failure
		// settling a charge the pool is full on.
		capacity = 1 << 20
		reserve  = 8 * 1024
	)
	client := budgetedBatchClientWith(t, transport, metaOptions{}, capacity, reserve)
	pool := client.budget
	failed := dialBatchConn(t, client)
	next := dialBatchConn(t, client)

	payload := bytes.Repeat([]byte("f"), capacity)
	if result := awaitPayloadWrite(t, writePayloadAsync(failed, payload), capacity); result.err != nil || result.n != capacity {
		t.Fatalf("failed stream Write = (%d, %v), want (%d, nil)", result.n, result.err, capacity)
	}
	waitForPool(t, pool, "main pool full", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})
	blocked := writePayloadAsync(next, payload)
	waitForPool(t, pool, "one queued stream", func(stats uplinkBudgetStats) bool {
		return stats.Waiters == 1
	})
	select {
	case result := <-blocked:
		t.Fatalf("the second stream's Write returned while the pool was full: (%d, %v)", result.n, result.err)
	default:
	}

	close(gate)
	if result := awaitPayloadWrite(t, blocked, capacity); result.err != nil || result.n != capacity {
		t.Fatalf("queued stream Write = (%d, %v), want (%d, nil)", result.n, result.err, capacity)
	}
	// The failed stream must report the loss through its own ledger and hold
	// nothing in the pool.
	failedStats := failed.(*packetConn).uplink.stats()
	if failedStats.SharedHeldBytes != 0 {
		t.Fatalf("failed stream still holds %d shared bytes", failedStats.SharedHeldBytes)
	}
	if failedStats.AbandonedBytes != uint64(capacity) || failedStats.PostedBytes != 0 {
		t.Fatalf("failed stream ledger = %+v, want all %d bytes abandoned and none posted", failedStats, capacity)
	}
	if err := failed.Close(); err != nil {
		t.Fatalf("Close failed stream: %v", err)
	}
	if _, err := failed.Write([]byte("after failure")); err == nil {
		t.Fatal("the failed stream accepted a write after its post failed")
	}
	if err := next.Close(); err != nil {
		t.Fatalf("Close next stream: %v", err)
	}
	final := waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if final.Used != 0 || final.ReservedUsed != 0 {
		t.Fatalf("pool not back at baseline: %+v", final)
	}
	if final.GrantedBytes != final.ReleasedBytes || final.ExcessReleases != 0 || final.LeftoverBytes != 0 {
		t.Fatalf("pool flow does not close: %+v", final)
	}
}

// TestPacketUpSharedBudgetConservesChargesUnderConcurrency runs several streams
// from several goroutines at once against a pool too small to hold them, sampling
// throughout, and then proves the pool returns to zero with no double release and
// no leftover. It is the -race target for the new locking.
func TestPacketUpSharedBudgetConservesChargesUnderConcurrency(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	transport.responseDelay = 200 * time.Microsecond
	client := budgetedBatchClientWith(t, transport, metaOptions{}, 64*1024, 16*1024)
	pool := client.budget

	const (
		streams       = 3
		writers       = 3
		chunks        = 12
		maxChunkBytes = 8 * 1024
	)
	conns := make([]net.Conn, 0, streams)
	for index := 0; index < streams; index++ {
		conns = append(conns, dialBatchConn(t, client))
	}
	var waitGroup sync.WaitGroup
	for index, conn := range conns {
		for writer := 0; writer < writers; writer++ {
			waitGroup.Add(1)
			go func(conn net.Conn, seed int) {
				defer waitGroup.Done()
				for chunk := 0; chunk < chunks; chunk++ {
					size := 1 + (seed*31+chunk*17)%maxChunkBytes
					if chunk%5 == 0 {
						// Larger than the reserve's size class: it must be charged
						// to the main pool, not the control path.
						size += packetUpSmallWrite
					}
					if _, err := conn.Write(bytes.Repeat([]byte{'x'}, size)); err != nil {
						t.Errorf("concurrent Write: %v", err)
						return
					}
				}
			}(conn, index*writers+writer)
		}
	}
	writersDone := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(writersDone)
	}()
	sampling := true
	for sampling {
		select {
		case <-writersDone:
			sampling = false
		default:
			stats := pool.stats()
			if stats.Used > stats.Capacity {
				t.Fatalf("main pool %d exceeds capacity %d under concurrency", stats.Used, stats.Capacity)
			}
			if stats.ReservedUsed > stats.Reserve {
				t.Fatalf("reserve %d exceeds its bound %d under concurrency", stats.ReservedUsed, stats.Reserve)
			}
			if stats.Peak > stats.Capacity+stats.Reserve {
				t.Fatalf("peak %d exceeds capacity+reserve %d under concurrency", stats.Peak, stats.Capacity+stats.Reserve)
			}
			for _, conn := range conns {
				streamStats := conn.(*packetConn).uplink.stats()
				if streamStats.SharedHeldBytes != streamStats.PendingBytes+streamStats.InFlightBytes {
					t.Fatalf("stream holds %d shared bytes but retains %d (pending %d + in-flight %d)",
						streamStats.SharedHeldBytes, streamStats.PendingBytes+streamStats.InFlightBytes,
						streamStats.PendingBytes, streamStats.InFlightBytes)
				}
			}
			time.Sleep(100 * time.Microsecond)
		}
	}
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Used+stats.ReservedUsed == 0
	})
	for _, conn := range conns {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	final := waitForPool(t, pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if final.ExcessReleases != 0 {
		t.Fatalf("%d bytes were released twice", final.ExcessReleases)
	}
	if final.LeftoverBytes != 0 {
		t.Fatalf("%d bytes were still charged when their flushers exited", final.LeftoverBytes)
	}
	if final.GrantedBytes != final.ReleasedBytes {
		t.Fatalf("granted %d != released %d", final.GrantedBytes, final.ReleasedBytes)
	}
}

// stubDialer satisfies N.Dialer for the constructor test; nothing in it is dialed,
// so every use is a failure the test would see.
type stubDialer struct{}

func (stubDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("stub dialer: no dial expected")
}

func (stubDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("stub dialer: no dial expected")
}

var _ N.Dialer = stubDialer{}

// TestNewClientWiresTheSharedUploadBudget proves the pool the other tests use is
// what production gets: NewClient is the only constructor the vless outbound
// reaches through the v2ray transport registry.
func TestNewClientWiresTheSharedUploadBudget(t *testing.T) {
	t.Parallel()

	transport, err := NewClient(
		context.Background(),
		stubDialer{},
		M.ParseSocksaddr("127.0.0.1:443"),
		option.V2RayXHTTPOptions{Mode: modePacketUp},
		nil,
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client := transport.(*Client)
	if client.budget == nil {
		t.Fatal("NewClient left the shared upload pool unset: packet-up streams would only have the per-stream bound")
	}
	stats := client.budget.stats()
	wantCapacity := packetUpSharedCapacity(packetUpPendingBound(client.meta))
	if stats.Capacity != wantCapacity {
		t.Fatalf("pool capacity %d, want %d (four streams' worth of the %d-byte per-stream bound, capped at %d)",
			stats.Capacity, wantCapacity, packetUpPendingBound(client.meta), packetUpSharedCeiling)
	}
	if stats.Reserve != packetUpSharedReserve {
		t.Fatalf("pool reserve %d, want %d", stats.Reserve, packetUpSharedReserve)
	}
	if stats.Capacity < packetUpPendingBound(client.meta) {
		t.Fatalf("pool capacity %d is below one stream's bound %d", stats.Capacity, packetUpPendingBound(client.meta))
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestPacketUpReserveIsForTheOpeningNotForBulkChunkedWrites is the audit's P1
// reproduction: a long flow writing its data in 8 KiB blocks used to drain the
// reserve because eligibility looked only at the size of one Write, so a new stream's
// 1 KiB first write then had to wait. Eligibility is now the flow's opening state:
// once a stream has written past packetUpReserveOpeningBytes, its chunks queue for the
// main pool like any bulk traffic, and the reserve stays available for openings.
func TestPacketUpReserveIsForTheOpeningNotForBulkChunkedWrites(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	gate := make(chan struct{})
	var entered sync.Once
	posted := make(chan struct{})
	transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
		entered.Do(func() { close(posted) })
		<-gate
		return okProbeResponse(), nil
	}
	const (
		capacity = 64 * 1024
		reserve  = 32 * 1024
	)
	client := budgetedBatchClientWith(t, transport, metaOptions{}, capacity, reserve)
	pool := client.budget
	bulk := dialBatchConn(t, client)

	// Fill the main pool with 8 KiB blocks; every block fits, so Write returns.
	chunk := make([]byte, 8*1024)
	for index := 0; index < 8; index++ {
		if result := awaitPayloadWrite(t, writePayloadAsync(bulk, chunk), len(chunk)); result.err != nil || result.n != len(chunk) {
			t.Fatalf("main-pool fill write %d = (%d, %v)", index, result.n, result.err)
		}
	}
	select {
	case <-posted:
	case <-time.After(writeFreeBudget):
		t.Fatal("no post started")
	}
	waitForPool(t, pool, "main pool full", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})

	// Past its opening, the same stream's next 8 KiB block must NOT touch the reserve:
	// it queues for the main pool.
	blocked := writePayloadAsync(bulk, chunk)
	waitForPool(t, pool, "the bulk chunk queued on the main pool", func(stats uplinkBudgetStats) bool {
		return stats.Waiters == 1
	})
	if stats := pool.stats(); stats.ReservedUsed != 0 {
		t.Fatalf("a bulk flow's chunked write drained the reserve: %+v", stats)
	}

	// A new stream's opening write still gets through, to the transport.
	control := dialBatchConn(t, client)
	opening := make([]byte, 1024)
	if result := awaitPayloadWrite(t, writePayloadAsync(control, opening), len(opening)); result.err != nil || result.n != len(opening) {
		t.Fatalf("opening write = (%d, %v), want (%d, nil)", result.n, result.err, len(opening))
	}
	waitForPool(t, pool, "the opening charged the reserve", func(stats uplinkBudgetStats) bool {
		return stats.ReservedUsed == len(opening)
	})

	close(gate)
	awaitPayloadWrite(t, blocked, len(chunk))
	waitForPool(t, pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Used+stats.ReservedUsed == 0
	})
}
