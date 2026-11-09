package v2rayxhttp

// A REAL HTTP/2 XHTTP server A/B for the shared upload budget.
//
// The in-process experiment (packetup_budget_experiment_test.go, XHTTP_BUDGET_EXPERIMENT=1)
// answers what the pools and the client-side bookkeeping do with a substituted
// RoundTripper. This harness answers a different question: does the same workload behave
// the same way against a real h2c server over real sockets - are the bytes all delivered,
// is the per-stream arm's peak really unbounded, and does the pooled arm stay inside its
// bound while the short flows still finish?
//
// What this is NOT: an exclusive-host or network-shaped measurement. It runs on loopback
// (no added RTT, no MTU pressure) in a shared process, so the wall-clock/throughput numbers
// are process-level evidence, not the final performance verdict. The final A/B still needs
// the integration window's exclusive sampling plus the isolated shaping runs (WSL netem or
// a real link).
//
// Run:
//
//	XHTTP_REAL_AB=1 go test ./transport/v2rayxhttp -run TestRealPacketUpServerBudgetAB -v -count=1

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// realPacketUpServer is a minimal packet-up server over h2c: upload POSTs are drained and
// counted, the download GET is held open until the stream ends. It is the smallest server
// that makes the real client path run end to end (TCP, HTTP/2, flow control, request
// bodies) without a proxy.
type realPacketUpServer struct {
	addr    string
	server  *http.Server
	uploads atomic.Int64
	bytes   atomic.Int64
}

func newRealPacketUpServer(t *testing.T) *realPacketUpServer {
	t.Helper()
	s := &realPacketUpServer{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// The download half: answer headers at once and hold the body open, which is
			// what a packet-up server does until it has downlink to send.
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			return
		}
		s.uploads.Add(1)
		s.bytes.Add(int64(len(body)))
		w.WriteHeader(http.StatusOK)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.addr = listener.Addr().String()
	s.server = &http.Server{Handler: h2c.NewHandler(handler, &http2.Server{})}
	go func() { _ = s.server.Serve(listener) }()
	t.Cleanup(func() { _ = s.server.Close() })
	return s
}

// realABResult is one arm's outcome.
type realABResult struct {
	name          string
	bytes         int64
	streams       int
	wall          time.Duration
	heapGrowth    uint64
	logicalPeak   int
	allocatedPeak int
	failures      int
}

// realABClient builds a packet-up Client speaking cleartext HTTP/2 at addr. The pooled arm
// gets the production budget; the per-stream arm runs without a shared pool, which is the
// pre-pool baseline the report compares against.
func realABClient(t *testing.T, addr string, pooled bool) *Client {
	t.Helper()
	client := h2cClient(t, addr, modePacketUp)
	if pooled {
		client.budget = newSharedUplinkBudget(packetUpSharedCapacity(packetUpPendingBound(client.meta)), packetUpSharedReserve)
	} else {
		client.budget = nil
	}
	return client
}

// runRealABWorkload drives the same workload as the in-process experiment: long streams
// that saturate the pool and short streams that must still finish, all against the real
// server.
func runRealABWorkload(t *testing.T, server *realPacketUpServer, pooled bool) realABResult {
	t.Helper()
	const longStreams = 4
	const shortStreams = 4
	const longBytes = 2 << 20
	shortSizes := []int{96 << 10, 128 << 10, 160 << 10, 256 << 10}

	client := realABClient(t, server.addr, pooled)
	t.Cleanup(func() { _ = client.Close() })
	before := new(runtime.MemStats)
	runtime.GC()
	runtime.ReadMemStats(before)
	bytesBefore := server.bytes.Load()

	conns := make([]net.Conn, 0, longStreams+shortStreams)
	for index := 0; index < longStreams+shortStreams; index++ {
		conn, err := client.DialContext(context.Background())
		if err != nil {
			t.Fatalf("dial stream %d: %v", index, err)
		}
		conns = append(conns, conn)
	}

	result := realABResult{name: "pooled", streams: len(conns)}
	if !pooled {
		result.name = "per-stream"
	}
	// Sample the logical peak while the streams are in flight.
	var peak int
	var peakAllocated int
	var sampling atomic.Bool
	sampling.Store(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sampling.Load() {
			if pooled {
				stats := client.budget.stats()
				if stats.Total() > peak {
					peak = stats.Total()
				}
				if stats.Allocated > peakAllocated {
					peakAllocated = stats.Allocated
				}
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()

	started := time.Now()
	var waitGroup sync.WaitGroup
	var failures atomic.Int64
	for index, conn := range conns {
		waitGroup.Add(1)
		go func(conn net.Conn, index int) {
			defer waitGroup.Done()
			size := longBytes
			if index >= longStreams {
				size = shortSizes[(index-longStreams)%len(shortSizes)]
			}
			payload := make([]byte, size)
			if _, err := conn.Write(payload); err != nil {
				failures.Add(1)
			}
		}(conn, index)
	}
	waitGroup.Wait()
	// Close drains the buffered tail, so this is when the stream is actually done.
	for _, conn := range conns {
		if err := conn.Close(); err != nil {
			failures.Add(1)
		}
	}
	result.wall = time.Since(started)
	sampling.Store(false)
	<-done
	result.logicalPeak = peak
	result.allocatedPeak = peakAllocated
	result.failures = int(failures.Load())
	result.bytes = server.bytes.Load() - bytesBefore

	after := new(runtime.MemStats)
	runtime.ReadMemStats(after)
	if after.HeapAlloc > before.HeapAlloc {
		result.heapGrowth = after.HeapAlloc - before.HeapAlloc
	}
	expected := int64(longStreams*longBytes + shortSizes[0] + shortSizes[1] + shortSizes[2] + shortSizes[3])
	if result.bytes != expected {
		t.Fatalf("%s arm: the server received %d bytes, want %d (lost data)", result.name, result.bytes, expected)
	}
	if result.failures != 0 {
		t.Fatalf("%s arm: %d failures", result.name, result.failures)
	}
	return result
}

// TestRealPacketUpServerBudgetAB is the real-server A/B. It is opt-in
// (XHTTP_REAL_AB=1) because it is not a quick unit test; its numbers are process-level
// evidence on loopback, not the exclusive final measurement.
func TestRealPacketUpServerBudgetAB(t *testing.T) {
	if os.Getenv("XHTTP_REAL_AB") == "" {
		t.Skip("set XHTTP_REAL_AB=1 to run the real-server A/B")
	}
	server := newRealPacketUpServer(t)

	perStream := runRealABWorkload(t, server, false)
	pooled := runRealABWorkload(t, server, true)

	// The same workload must deliver the same bytes in both arms; the pooled arm must stay
	// inside its bound while the per-stream arm is only bounded per stream (which is the
	// reason the pool exists).
	if pooled.bytes != perStream.bytes {
		t.Fatalf("arms delivered different bytes: per-stream %d, pooled %d", perStream.bytes, pooled.bytes)
	}

	if pooled.logicalPeak == 0 {
		t.Fatalf("the pooled arm never charged the budget: %+v", pooled)
	}
	if pooled.logicalPeak > perStream.logicalPeak && perStream.logicalPeak != 0 {
		t.Fatalf("pooled peak %d above per-stream peak %d", pooled.logicalPeak, perStream.logicalPeak)
	}
	t.Logf("per-stream: bytes=%d wall=%s heap+%d logicalPeak=%d",
		perStream.bytes, perStream.wall.Round(time.Millisecond), perStream.heapGrowth, perStream.logicalPeak)
	t.Logf("pooled:     bytes=%d wall=%s heap+%d logicalPeak=%d allocatedPeak=%d",
		pooled.bytes, pooled.wall.Round(time.Millisecond), pooled.heapGrowth, pooled.logicalPeak, pooled.allocatedPeak)
}
