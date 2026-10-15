package v2rayxhttp

import (
	"sync"
	"testing"
	"time"
)

// lx: XHTTP high-RTT response-pipeline probe.
//
// The question this file answers: does waiting for a POST's full response before
// starting the next one cost single-stream packet-up throughput at high RTT?
//
// Measured live first (harness, TLS/HTTP-2, packet-up, one stream, the same
// reference server, three repetitions per point, 30 runs and 0 payload
// mismatches):
//
//	RTT     0      30      50      100     200   (ms)
//	fork    26.9   24.3    15.6    7.9     3.9   (MB/s)
//	xray    22.8   19.7    14.5    7.8     4.0   (MB/s)
//	ratio   1.18x  1.23x   1.07x   1.01x   0.97x
//
// Both implementations collapse together, and the reference already implements the
// proposed change: its packet-up drainer starts the next POST on httptrace
// WroteRequest and drains the response in a goroutine
// (transport/internet/splithttp/dialer.go:495-534). It therefore has no advantage
// at any RTT, which makes the collapse "one POST per round trip" — the post's own
// trip through the link — rather than "an extra wait the reference avoids".
//
// This probe pins the fork's side of that mechanism and the invariant that keeps
// the design safe: at every simulated round trip the flusher has at most ONE post
// in flight, its seq stays contiguous and ascending, and its cadence tracks the
// post's round trip.
func TestPacketUpPostCadenceTracksTheRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("timing probe")
	}
	delays := []time.Duration{0, 30 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond}
	const window = 1500 * time.Millisecond
	chunk := make([]byte, 256*1024)

	for _, delay := range delays {
		delay := delay
		t.Run(delay.String(), func(t *testing.T) {
			transport := newProbeTransport()
			transport.responseDelay = delay
			conn := dialBatchConn(t, stubClient(t, modePacketUp, transport))

			// A producer that never lets the queue empty: the benchmark's shape,
			// where the flusher is the constraint rather than the writer.
			stop := make(chan struct{})
			written := make(chan int64, 1)
			var once sync.Once
			finish := func() { once.Do(func() { close(stop) }) }
			go func() {
				var total int64
				for {
					select {
					case <-stop:
						written <- total
						return
					default:
					}
					count, err := conn.Write(chunk)
					total += int64(count)
					if err != nil {
						written <- total
						return
					}
				}
			}()

			time.Sleep(window)
			finish()
			total := <-written

			bodies, paths, posts, maxInflight := transport.snapshot()
			perSecond := float64(posts) / window.Seconds()
			bytesPerPost := 0
			if posts > 0 {
				bytesPerPost = len(bodies[0])
			}
			t.Logf("round trip %-8s posts=%3d (%5.1f/s) bytes/post=%6d maxInflight=%d written=%6.1f MB",
				delay, posts, perSecond, bytesPerPost, maxInflight, float64(total)/1e6)

			// The invariant the design rests on. A change that pipelines response
			// completion on purpose must come here and argue for a different bound,
			// which is exactly the point of pinning it.
			if maxInflight > 1 {
				t.Fatalf("max in-flight posts = %d, want at most 1", maxInflight)
			}
			for index, requestPath := range paths {
				if seq := seqOf(t, requestPath); seq != index {
					t.Fatalf("post %d carries seq %d: the flusher reordered or skipped a seq", index, seq)
				}
			}

			// Cadence: with a non-empty queue the next post starts when the previous
			// one's response lands, so the rate cannot exceed one post per round trip
			// (+ the anti-burst interval when that is larger). Asserted as an upper
			// bound with slack for scheduling, not as an equality.
			if posts > 2 {
				cadence := window / time.Duration(posts)
				floor := delay
				if floor < 30*time.Millisecond {
					floor = 30 * time.Millisecond // the default anti-burst interval
				}
				if cadence < floor/2 {
					t.Fatalf("cadence %s is faster than half the %s floor: the flusher is not gated by the round trip",
						cadence.Round(time.Millisecond), floor)
				}
			}

			if err := conn.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
		})
	}
}
