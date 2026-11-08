package v2rayxhttp

import (
	"net"
	"sync"
	"testing"
	"time"
)

// TestPacketUpConcurrentWritersAndClose is a partial stage-11 concurrency audit of the byte-budget
// path that DOES exist today (stage 03): many writers contending for the queue while the conn is
// closed must neither deadlock, nor exceed the bound, nor leave the queue accounting inconsistent.
//
// It is deliberately scoped: the full V02 audit needs the residency/shared-task mechanisms from
// stages 04-09, which are not implemented. This test only pins the implemented slice.
func TestPacketUpConcurrentWritersAndClose(t *testing.T) {
	t.Parallel()
	transport := newRecordingTransport(false)
	client := batchClient(t, transport, metaOptions{ScMaxEachPostBytes: "30000-30000"})
	conn := dialBatchConn(t, client)
	packet := conn.(*packetConn)

	const (
		writers    = 8
		iterations = 40
		chunkSize  = 4 * 1024
	)
	started := make(chan struct{})
	var group sync.WaitGroup
	group.Add(writers)
	for index := 0; index < writers; index++ {
		go func() {
			defer group.Done()
			<-started
			chunk := make([]byte, chunkSize)
			for iteration := 0; iteration < iterations; iteration++ {
				n, err := conn.Write(chunk)
				if err != nil {
					// Close wins: a writer that was already past the closed check may see either
					// the close error or a successful append. Both are legal; nothing may hang.
					if err != net.ErrClosed {
						t.Errorf("unexpected write error: %v", err)
					}
					return
				}
				if n != chunkSize {
					t.Errorf("short write without error: n=%d want %d", n, chunkSize)
					return
				}
			}
		}()
	}
	close(started)
	// Give the writers a moment to contend, then close underneath them.
	time.Sleep(10 * time.Millisecond)
	closeErr := conn.Close()
	if closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("writers did not finish after Close: possible deadlock on the queue condition")
	}

	stats := packet.uplink.stats()
	if stats.PendingBytes < 0 || stats.PendingBytes > stats.MaxPending {
		t.Fatalf("pending %d outside [0,%d]", stats.PendingBytes, stats.MaxPending)
	}
	if stats.PendingHighWater > stats.MaxPending {
		t.Fatalf("pending high-water %d exceeded the bound %d", stats.PendingHighWater, stats.MaxPending)
	}
	// Close drains what it accepted; with an instant transport every accepted byte is posted.
	deadline := time.Now().Add(5 * time.Second)
	for {
		stats = packet.uplink.stats()
		if stats.PendingBytes == 0 && stats.InFlightBytes == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue did not drain after Close: pending=%d in-flight=%d", stats.PendingBytes, stats.InFlightBytes)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if stats.PostedBytes != stats.AcceptedBytes {
		t.Fatalf("posted %d != accepted %d after a clean drain", stats.PostedBytes, stats.AcceptedBytes)
	}
}
