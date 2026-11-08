package v2rayxhttp

import (
	"testing"
	"time"
)

// TestPacketUpOversizedWriteIsBounded pins the budget against the shape it used
// to miss: one Write larger than the whole queue. The write must be accepted in
// chunks, so the queue never exceeds its bound, while the call still returns the
// full length (callers that treat a short write as an error keep working).
func TestPacketUpOversizedWriteIsBounded(t *testing.T) {
	t.Parallel()
	transport := newRecordingTransport(true)
	client := batchClient(t, transport, metaOptions{ScMaxEachPostBytes: "30000-30000"})
	conn := dialBatchConn(t, client)
	packet := conn.(*packetConn)

	if _, err := conn.Write([]byte("start")); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	select {
	case <-transport.entered:
	case <-time.After(writeFreeBudget):
		t.Fatal("the first write never reached a post")
	}

	bound := packet.uplink.maxPending
	oversized := make([]byte, bound*2)
	type writeResult struct {
		n   int
		err error
	}
	result := make(chan writeResult, 1)
	go func() {
		n, err := conn.Write(oversized)
		result <- writeResult{n: n, err: err}
	}()

	// The queue must fill to its bound and stay there while the gated post holds
	// the flusher.
	deadline := time.Now().Add(writeFreeBudget)
	for packet.uplink.stats().PendingBytes < bound {
		if time.Now().After(deadline) {
			t.Fatalf("queue never filled to its bound: %+v", packet.uplink.stats())
		}
		time.Sleep(2 * time.Millisecond)
	}
	stats := packet.uplink.stats()
	if stats.PendingBytes != bound {
		t.Fatalf("pending %d, want the bound %d", stats.PendingBytes, bound)
	}
	select {
	case got := <-result:
		t.Fatalf("oversized Write returned while the queue was full: n=%d err=%v", got.n, got.err)
	case <-time.After(100 * time.Millisecond):
	}

	close(transport.release)
	select {
	case got := <-result:
		if got.err != nil || got.n != len(oversized) {
			t.Fatalf("oversized Write: n=%d err=%v, want n=%d err=nil", got.n, got.err, len(oversized))
		}
	case <-time.After(writeFreeBudget):
		t.Fatal("oversized Write never completed after the post was released")
	}
	stats = packet.uplink.stats()
	if stats.PendingHighWater > stats.MaxPending {
		t.Fatalf("pending high-water %d exceeded the bound %d", stats.PendingHighWater, stats.MaxPending)
	}
}

// TestPacketUpAccountsInFlightSeparately proves the two-part bound: a post owns
// its slice after it leaves the queue, so pending alone understates what the
// stream retains. The snapshot must report both.
func TestPacketUpAccountsInFlightSeparately(t *testing.T) {
	t.Parallel()
	transport := newRecordingTransport(true)
	client := batchClient(t, transport, metaOptions{ScMaxEachPostBytes: "30000-30000"})
	conn := dialBatchConn(t, client)
	packet := conn.(*packetConn)

	if _, err := conn.Write([]byte("start")); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	select {
	case <-transport.entered:
	case <-time.After(writeFreeBudget):
		t.Fatal("the first write never reached a post")
	}
	stats := packet.uplink.stats()
	if stats.InFlightBytes != len("start") {
		t.Fatalf("in-flight %d, want %d", stats.InFlightBytes, len("start"))
	}
	if stats.PendingBytes != 0 {
		t.Fatalf("pending %d, want 0 while the first post owns its slice", stats.PendingBytes)
	}

	// Fill the queue behind the gated post: both numbers are now non-zero and
	// their sum is the real retention.
	chunk := make([]byte, packet.uplink.maxPending)
	if wrote, err := conn.Write(chunk); err != nil || wrote != len(chunk) {
		t.Fatalf("fill Write: n=%d err=%v", wrote, err)
	}
	stats = packet.uplink.stats()
	if stats.PendingBytes != len(chunk) || stats.InFlightBytes != len("start") {
		t.Fatalf("snapshot pending=%d in-flight=%d, want %d and %d",
			stats.PendingBytes, stats.InFlightBytes, len(chunk), len("start"))
	}

	close(transport.release)
	deadline := time.Now().Add(writeFreeBudget)
	for {
		stats = packet.uplink.stats()
		if stats.PendingBytes == 0 && stats.InFlightBytes == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue did not drain: pending=%d in-flight=%d", stats.PendingBytes, stats.InFlightBytes)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if stats.PostedBytes != stats.AcceptedBytes {
		t.Fatalf("posted %d, accepted %d: every accepted byte should have been posted", stats.PostedBytes, stats.AcceptedBytes)
	}
	if stats.PendingHighWater > stats.MaxPending {
		t.Fatalf("pending high-water %d exceeded the bound %d", stats.PendingHighWater, stats.MaxPending)
	}
}
