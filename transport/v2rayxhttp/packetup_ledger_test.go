package v2rayxhttp

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// V01A — the packet-up byte ledger, exercised from the real dial entry over a
// post that can be held, failed and closed underneath a write.
//
// The uplink has four buckets and one identity:
//
//	accepted = posted + abandoned + pending + in-flight
//
// Testing the identity rather than individual fields is the point: a missing
// bucket is exactly how bytes disappear. Stage 03 landed posted, pending and
// in-flight; a failed post then left its own slice in neither (it was cleared
// from in-flight without being posted) while the queue behind it stayed in
// pending forever — reported as deliverable work by stats(), retained in memory,
// and never posted. The abandoned bucket (and dropping the dead queue) is what
// this file pins, together with the bound, the zeroing, the worker's exit, and
// the session's usability afterwards.

// uplinkLedgerError states the ledger's invariant as a plain function so the
// negative control below can prove the assertions reject a broken ledger,
// instead of only observing that a healthy one passes.
func uplinkLedgerError(stats packetUplinkStats) error {
	if stats.PendingBytes < 0 || stats.InFlightBytes < 0 {
		return fmt.Errorf("negative queue accounting: pending %d, in-flight %d",
			stats.PendingBytes, stats.InFlightBytes)
	}
	if stats.PendingBytes > stats.MaxPending || stats.PendingHighWater > stats.MaxPending {
		return fmt.Errorf("queue exceeded its bound: pending %d, high-water %d, bound %d",
			stats.PendingBytes, stats.PendingHighWater, stats.MaxPending)
	}
	if stats.InFlightBytes > stats.MaxPending {
		return fmt.Errorf("in-flight %d exceeds the whole queue budget %d: one post owns more than the queue may hold",
			stats.InFlightBytes, stats.MaxPending)
	}
	accounted := stats.PostedBytes + stats.AbandonedBytes + uint64(stats.PendingBytes) + uint64(stats.InFlightBytes)
	if accounted != stats.AcceptedBytes {
		return fmt.Errorf("ledger does not close: accepted %d, accounted %d "+
			"(posted %d + abandoned %d + pending %d + in-flight %d) = %d bytes unaccounted for",
			stats.AcceptedBytes, accounted, stats.PostedBytes, stats.AbandonedBytes,
			stats.PendingBytes, stats.InFlightBytes, int64(stats.AcceptedBytes)-int64(accounted))
	}
	return nil
}

func requireUplinkLedger(t *testing.T, stats packetUplinkStats) {
	t.Helper()
	if err := uplinkLedgerError(stats); err != nil {
		t.Fatal(err)
	}
}

// waitForUplinkWorker waits for the flusher to exit. It is the "worker exit"
// half of the acceptance: a queue that looks drained while its goroutine still
// waits on the condition is a session that will never finish.
func waitForUplinkWorker(t *testing.T, uplink *packetUplink) {
	t.Helper()
	select {
	case <-uplink.done:
	case <-time.After(writeFreeBudget):
		t.Fatal("the uplink flusher is still running: the queue never reached its exit condition")
	}
}

// TestPacketUpLedgerClosesAcrossAPostFailure is the regression guard for the
// stranded queue.
//
// Before the fix, a refused post produced this snapshot, measured:
//
//	{MaxPending:65536 PendingBytes:3072 InFlightBytes:0
//	 AcceptedBytes:3080 PostedBytes:0}
//
// — 3072 bytes that nothing would ever post (the flusher had exited), 8 bytes
// that left in-flight and never entered posted, and no bucket to say where
// either went. The test asserts the three consequences separately: the bytes
// are accounted as abandoned, the queue is released, and the failure is still
// reported to every later write without ever issuing another post.
func TestPacketUpLedgerClosesAcrossAPostFailure(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	entered := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	refused := errors.New("post refused by the transport")
	transport.onPost = func(request *http.Request, body []byte) (*http.Response, error) {
		if seqOf(t, request.URL.Path) == 0 {
			// Only the FIRST session's opening post is held and refused; the
			// session dialled later in this test must be able to post normally.
			var refusedHere bool
			once.Do(func() {
				refusedHere = true
				close(entered)
			})
			if refusedHere {
				<-gate
				return nil, refused
			}
		}
		return okProbeResponse(), nil
	}
	client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0", ScMaxEachPostBytes: "30000-30000"})
	conn := dialBatchConn(t, client)
	packet := conn.(*packetConn)

	inflight := []byte("inflight")
	if _, err := conn.Write(inflight); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(writeFreeBudget):
		t.Fatal("the first post never started")
	}
	queued := bytes.Repeat([]byte("q"), 3*1024)
	if _, err := conn.Write(queued); err != nil {
		t.Fatalf("queued Write: %v", err)
	}

	// Peak: the post owns its slice while the queue behind it holds the rest.
	peak := packet.uplink.stats()
	requireUplinkLedger(t, peak)
	if peak.InFlightBytes != len(inflight) || peak.PendingBytes != len(queued) {
		t.Fatalf("peak snapshot in-flight=%d pending=%d, want %d and %d",
			peak.InFlightBytes, peak.PendingBytes, len(inflight), len(queued))
	}
	if peak.InFlightHighWater != len(inflight) || peak.PendingHighWater != len(queued) {
		t.Fatalf("peak high-water in-flight=%d pending=%d, want %d and %d",
			peak.InFlightHighWater, peak.PendingHighWater, len(inflight), len(queued))
	}

	close(gate)

	// The failure must zero the queue, not leave it stranded.
	deadline := time.Now().Add(writeFreeBudget)
	var stats packetUplinkStats
	for {
		stats = packet.uplink.stats()
		if stats.PendingBytes == 0 && stats.InFlightBytes == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the failed post the ledger still holds pending=%d in-flight=%d "+
				"(accepted=%d posted=%d abandoned=%d): the stranded queue will never be posted "+
				"and never be reported as lost",
				stats.PendingBytes, stats.InFlightBytes, stats.AcceptedBytes, stats.PostedBytes, stats.AbandonedBytes)
		}
		time.Sleep(2 * time.Millisecond)
	}
	requireUplinkLedger(t, stats)
	wantAbandoned := uint64(len(inflight) + len(queued))
	if stats.AbandonedBytes != wantAbandoned || stats.AcceptedBytes != wantAbandoned {
		t.Fatalf("after the failure accepted=%d abandoned=%d, want %d abandoned (the post's own slice "+
			"plus the queue behind it) and nothing posted",
			stats.AcceptedBytes, stats.AbandonedBytes, wantAbandoned)
	}
	if stats.PostedBytes != 0 {
		t.Fatalf("posted %d bytes although the only post was refused", stats.PostedBytes)
	}
	// Releasing the array, not just logically emptying it: a dead queue that
	// keeps cap(pending) bytes is the retention half of the same defect.
	if stats.RetainedCapacity != 0 {
		t.Fatalf("the dead queue still retains %d bytes of array capacity", stats.RetainedCapacity)
	}
	waitForUplinkWorker(t, packet.uplink)

	// The refusal is reported, and it is reported instead of retrying into a
	// sequence hole.
	if _, err := conn.Write([]byte("after")); err == nil {
		t.Fatal("a write after the failed post succeeded: the failure was not surfaced")
	} else if !strings.Contains(err.Error(), "post refused") {
		t.Fatalf("the write path reported %v, want the transport's refusal", err)
	}
	// A zero-byte write is success by the io.Writer contract even on a dead conn,
	// and must not resurrect the queue.
	if n, err := conn.Write(nil); n != 0 || err != nil {
		t.Fatalf("Write(nil) after a failure = (%d, %v), want (0, nil)", n, err)
	}
	bodies, _, posts, maxInflight := transport.snapshot()
	if posts != 1 {
		t.Fatalf("saw %d posts, want exactly 1: a post after the failure would continue the sequence past the hole", posts)
	}
	if maxInflight != 1 {
		t.Fatalf("saw %d concurrent posts, want 1: packet-up uploads are sequential by contract", maxInflight)
	}
	if !bytes.Contains(bodies[0], inflight) {
		t.Fatalf("the refused post carried %q, want it to carry %q", bodies[0], inflight)
	}

	// Teardown stays with the caller, exactly once.
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if usage := openUsageOf(client); usage != 0 {
		t.Fatalf("openUsage = %d after Close, want 0 (leaked or double released)", usage)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if usage := openUsageOf(client); usage != 0 {
		t.Fatalf("openUsage = %d after a second Close, want 0", usage)
	}

	// Subsequent usability: a failure kills one session's upload half, not the
	// client. The next session must post its bytes and close with a clean ledger.
	second := dialBatchConn(t, client)
	secondPacket := second.(*packetConn)
	payload := []byte("second session")
	if _, err := second.Write(payload); err != nil {
		t.Fatalf("write on the session after a failure: %v", err)
	}
	transport.waitForPosts(t, 2)
	if err := second.Close(); err != nil {
		t.Fatalf("Close of the session after a failure: %v", err)
	}
	secondStats := secondPacket.uplink.stats()
	requireUplinkLedger(t, secondStats)
	if secondStats.AcceptedBytes != uint64(len(payload)) || secondStats.PostedBytes != secondStats.AcceptedBytes {
		t.Fatalf("the session after a failure accepted %d posted %d abandoned %d pending %d, want every "+
			"accepted byte posted",
			secondStats.AcceptedBytes, secondStats.PostedBytes, secondStats.AbandonedBytes, secondStats.PendingBytes)
	}
	if secondStats.AbandonedBytes != 0 {
		t.Fatalf("the healthy session abandoned %d bytes", secondStats.AbandonedBytes)
	}
	waitForUplinkWorker(t, secondPacket.uplink)
}

// TestPacketUpLedgerClosesWhenCloseInterruptsABlockedWrite pins the close
// ordering against a writer that is parked on a full queue: Close drains what it
// accepted (the buffered tail is not dropped), releases the parked writer with a
// partial count and net.ErrClosed, and leaves nothing abandoned.
func TestPacketUpLedgerClosesWhenCloseInterruptsABlockedWrite(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	entered := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	transport.onPost = func(request *http.Request, body []byte) (*http.Response, error) {
		if seqOf(t, request.URL.Path) == 0 {
			once.Do(func() { close(entered) })
			<-gate
		}
		return okProbeResponse(), nil
	}
	client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0", ScMaxEachPostBytes: "30000-30000"})
	conn := dialBatchConn(t, client)
	packet := conn.(*packetConn)
	bound := packet.uplink.maxPending

	head := []byte("head")
	if _, err := conn.Write(head); err != nil {
		t.Fatalf("head Write: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(writeFreeBudget):
		t.Fatal("the head post never started")
	}

	// Fill the queue to its bound while the post is held.
	fill := bytes.Repeat([]byte("f"), bound)
	if n, err := conn.Write(fill); err != nil || n != len(fill) {
		t.Fatalf("fill Write: n=%d err=%v, want n=%d", n, err, len(fill))
	}

	// An oversized write has no room and parks.
	type writeResult struct {
		n   int
		err error
	}
	blocked := make(chan writeResult, 1)
	go func() {
		n, err := conn.Write(bytes.Repeat([]byte("x"), 2*bound))
		blocked <- writeResult{n: n, err: err}
	}()
	select {
	case got := <-blocked:
		t.Fatalf("an oversized Write returned while the queue was full: n=%d err=%v", got.n, got.err)
	case <-time.After(100 * time.Millisecond):
	}
	requireUplinkLedger(t, packet.uplink.stats())

	// Close must not deadlock against the parked writer: it stops accepting,
	// drains what it owns, then returns.
	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close() }()
	deadline := time.Now().Add(writeFreeBudget)
	for {
		packet.uplink.access.Lock()
		stopAccepting := packet.uplink.closed
		packet.uplink.access.Unlock()
		if stopAccepting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close never stopped the queue accepting writes")
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)

	select {
	case got := <-blocked:
		// Close won the race, so the parked write must report a partial count
		// with net.ErrClosed rather than either hanging or claiming success.
		if !errors.Is(got.err, net.ErrClosed) {
			t.Fatalf("the write parked on a full queue returned err=%v after Close, want net.ErrClosed", got.err)
		}
		if got.n < 0 || got.n > 2*bound {
			t.Fatalf("the parked write reported n=%d for a %d-byte call", got.n, 2*bound)
		}
	case <-time.After(writeFreeBudget):
		t.Fatal("the write parked on a full queue was not released by Close: deadlock on the queue condition")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(packetUpDrainTimeout + writeFreeBudget):
		t.Fatal("Close did not return after draining")
	}

	stats := packet.uplink.stats()
	requireUplinkLedger(t, stats)
	if stats.PendingBytes != 0 || stats.InFlightBytes != 0 {
		t.Fatalf("after Close and a completed drain: pending=%d in-flight=%d, want 0",
			stats.PendingBytes, stats.InFlightBytes)
	}
	if stats.AbandonedBytes != 0 {
		t.Fatalf("Close abandoned %d bytes although the path was healthy: the buffered tail was dropped",
			stats.AbandonedBytes)
	}
	if stats.PostedBytes != stats.AcceptedBytes {
		t.Fatalf("posted %d of %d accepted bytes: Close dropped the tail", stats.PostedBytes, stats.AcceptedBytes)
	}
	// Every accepted byte reached the wire, in order, as one stream.
	bodies, _, posts, maxInflight := transport.snapshot()
	var reassembled []byte
	for _, body := range bodies {
		reassembled = append(reassembled, body...)
	}
	want := append(append([]byte{}, head...), fill...)
	if !bytes.Equal(reassembled, want) {
		t.Fatalf("the posts carried %d bytes, want %d: the close-drained tail does not match what was accepted",
			len(reassembled), len(want))
	}
	if posts == 0 || maxInflight != 1 {
		t.Fatalf("saw %d posts with %d concurrent, want sequential posts", posts, maxInflight)
	}
	waitForUplinkWorker(t, packet.uplink)

	// Writes after the drain are refused, and a second Close is a no-op.
	if n, err := conn.Write([]byte("after close")); n != 0 || !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write after Close = (%d, %v), want (0, net.ErrClosed)", n, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if usage := openUsageOf(client); usage != 0 {
		t.Fatalf("openUsage = %d after Close, want 0", usage)
	}
}

// TestPacketUpNilWriteAndWritesAfterClose pins the two degenerate paths the
// budget tests do not reach: an empty write must not queue, signal the flusher
// or count a byte, and every write after Close must report the close rather than
// pretending the bytes were accepted.
func TestPacketUpNilWriteAndWritesAfterClose(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0"})
	conn := dialBatchConn(t, client)
	packet := conn.(*packetConn)

	if n, err := conn.Write(nil); n != 0 || err != nil {
		t.Fatalf("Write(nil) = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := conn.Write([]byte{}); n != 0 || err != nil {
		t.Fatalf("Write([]byte{}) = (%d, %v), want (0, nil)", n, err)
	}
	// An empty write must not start a post: the next post must carry only the
	// real payload, with no phantom empty batch in front of it.
	if _, err := conn.Write([]byte("real")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	bodies, _, _, _ := transport.snapshot()
	if len(bodies) != 1 || string(bodies[0]) != "real" {
		t.Fatalf("posts carried %q, want exactly one post carrying %q", bodies, "real")
	}
	stats := packet.uplink.stats()
	requireUplinkLedger(t, stats)
	if stats.AcceptedBytes != uint64(len("real")) || stats.PostedBytes != stats.AcceptedBytes {
		t.Fatalf("accepted=%d posted=%d, want %d and %d", stats.AcceptedBytes, stats.PostedBytes,
			len("real"), len("real"))
	}

	if n, err := conn.Write([]byte("late")); n != 0 || !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write after Close = (%d, %v), want (0, net.ErrClosed)", n, err)
	}
	// Zero-byte writes remain legal: n == len(p) is success by the io.Writer
	// contract, so this is not the closed path failing to report.
	if n, err := conn.Write(nil); n != 0 || err != nil {
		t.Fatalf("Write(nil) after Close = (%d, %v), want (0, nil)", n, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if usage := openUsageOf(client); usage != 0 {
		t.Fatalf("openUsage = %d after Close, want 0", usage)
	}
	waitForUplinkWorker(t, packet.uplink)
	stats = packet.uplink.stats()
	requireUplinkLedger(t, stats)
	if stats.PendingBytes != 0 || stats.InFlightBytes != 0 || stats.AbandonedBytes != 0 {
		t.Fatalf("final ledger is not zeroed: pending=%d in-flight=%d abandoned=%d",
			stats.PendingBytes, stats.InFlightBytes, stats.AbandonedBytes)
	}
}

// TestPacketUpLedgerCheckRejectsABrokenLedger is the negative control: the
// identity assertions in the tests above are only worth their failures if the
// check can fail. Each snapshot here is a ledger a plausible implementation
// would produce — including the exact pre-fix shape the stranded-queue test
// above reproduces — and every one of them must be rejected.
func TestPacketUpLedgerCheckRejectsABrokenLedger(t *testing.T) {
	t.Parallel()

	const bound = 65536
	broken := []struct {
		name  string
		stats packetUplinkStats
	}{
		{
			// The pre-fix failure snapshot: the post's 8 bytes left in-flight
			// without entering posted, and the queue behind it stayed pending.
			name:  "the_failed_post_lost_its_own_slice",
			stats: packetUplinkStats{MaxPending: bound, AcceptedBytes: 3080, PostedBytes: 0, PendingBytes: 3072, InFlightBytes: 0},
		},
		{
			// Abandoned bytes folded away entirely: a ledger that zeroes the queue
			// by forgetting it would look clean and lose 3080 bytes.
			name:  "the_whole_loss_is_unaccounted_for",
			stats: packetUplinkStats{MaxPending: bound, AcceptedBytes: 3080},
		},
		{
			name:  "abandoned_is_double_counted",
			stats: packetUplinkStats{MaxPending: bound, AcceptedBytes: 100, PostedBytes: 100, AbandonedBytes: 40},
		},
		{
			name:  "pending_exceeds_the_bound",
			stats: packetUplinkStats{MaxPending: 16, AcceptedBytes: 32, PendingBytes: 32},
		},
		{
			name:  "pending_high_water_exceeds_the_bound",
			stats: packetUplinkStats{MaxPending: 16, AcceptedBytes: 16, PostedBytes: 16, PendingHighWater: 17},
		},
		{
			name:  "in_flight_exceeds_the_whole_budget",
			stats: packetUplinkStats{MaxPending: 16, AcceptedBytes: 32, InFlightBytes: 32},
		},
		{
			name:  "negative_pending",
			stats: packetUplinkStats{MaxPending: 16, AcceptedBytes: 0, PendingBytes: -1},
		},
	}
	for _, testCase := range broken {
		t.Run(testCase.name, func(t *testing.T) {
			if err := uplinkLedgerError(testCase.stats); err == nil {
				t.Fatalf("the ledger check accepted a broken snapshot: %+v", testCase.stats)
			}
		})
	}

	// Positive control: the same check must accept a closed ledger that carries a
	// real loss, or the rejections above could be satisfied by a check that
	// rejects everything.
	if err := uplinkLedgerError(packetUplinkStats{MaxPending: bound, AcceptedBytes: 100, PostedBytes: 60, AbandonedBytes: 40}); err != nil {
		t.Fatalf("the ledger check rejected a closed ledger with a real loss: %v", err)
	}
}
