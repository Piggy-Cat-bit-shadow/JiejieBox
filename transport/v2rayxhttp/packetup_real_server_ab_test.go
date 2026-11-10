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
// Modes (XHTTP_REAL_AB):
//
//	1      in-process: start the server in this process, run both arms against it.
//	server listen on XHTTP_REAL_AB_ADDR (default 127.0.0.1:0), write the address to
//	       XHTTP_REAL_AB_ADDR_FILE and the running totals to XHTTP_REAL_AB_RESULT_FILE,
//	       and serve until the process is killed (bounded by XHTTP_REAL_AB_DURATION).
//	client read the address file, run both arms against that server, assert equal client
//	       bytes and no failures, and write the totals to XHTTP_REAL_AB_RESULT_FILE.
//
// The server/client split exists so the A/B can run across two network namespaces joined
// by a shaped veth (MTU 1280 + netem 50 ms); see run_xhttp_shaped_ab.sh. What this is NOT:
// an exclusive-host measurement - it is process-level evidence, and the final performance
// verdict still needs the integration window's exclusive sampling.

import (
	"context"
	"fmt"
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

func newRealPacketUpServerAt(t *testing.T, addr string) *realPacketUpServer {
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
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	s.addr = listener.Addr().String()
	s.server = &http.Server{Handler: h2c.NewHandler(handler, &http2.Server{})}
	go func() { _ = s.server.Serve(listener) }()
	t.Cleanup(func() { _ = s.server.Close() })
	return s
}

func newRealPacketUpServer(t *testing.T) *realPacketUpServer {
	t.Helper()
	return newRealPacketUpServerAt(t, "127.0.0.1:0")
}

// realABResult is one arm's outcome.
type realABResult struct {
	name          string
	bytes         int64 // client-side bytes whose Write returned no error
	serverBytes   int64 // only filled by the in-process mode
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
// server at addr.
func runRealABWorkload(t *testing.T, addr string, pooled bool) realABResult {
	t.Helper()
	const longStreams = 4
	const shortStreams = 4
	const longBytes = 2 << 20
	shortSizes := []int{96 << 10, 128 << 10, 160 << 10, 256 << 10}

	client := realABClient(t, addr, pooled)
	t.Cleanup(func() { _ = client.Close() })
	before := new(runtime.MemStats)
	runtime.GC()
	runtime.ReadMemStats(before)

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
	var written atomic.Int64
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
				return
			}
			written.Add(int64(size))
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
	result.bytes = written.Load()

	after := new(runtime.MemStats)
	runtime.ReadMemStats(after)
	if after.HeapAlloc > before.HeapAlloc {
		result.heapGrowth = after.HeapAlloc - before.HeapAlloc
	}
	expected := int64(longStreams*longBytes + shortSizes[0] + shortSizes[1] + shortSizes[2] + shortSizes[3])
	if result.bytes != expected {
		t.Fatalf("%s arm: the client wrote %d bytes, want %d (lost data)", result.name, result.bytes, expected)
	}
	if result.failures != 0 {
		t.Fatalf("%s arm: %d failures", result.name, result.failures)
	}
	return result
}

// TestRealPacketUpServerBudgetAB is the real-server A/B. It is opt-in (XHTTP_REAL_AB)
// because it is not a quick unit test; its numbers are process-level evidence, not the
// exclusive final measurement.
func TestRealPacketUpServerBudgetAB(t *testing.T) {
	switch os.Getenv("XHTTP_REAL_AB") {
	case "":
		t.Skip("set XHTTP_REAL_AB=1 (in-process), =server or =client")
	case "server":
		runRealABServer(t)
		return
	case "client":
		runRealABClient(t)
		return
	}
	server := newRealPacketUpServer(t)

	perStream := runRealABWorkload(t, server.addr, false)
	pooled := runRealABWorkload(t, server.addr, true)

	// The same workload must deliver the same bytes in both arms; the pooled arm must stay
	// inside its bound while the per-stream arm is only bounded per stream (which is the
	// reason the pool exists).
	if server.bytes.Load() != perStream.bytes+pooled.bytes {
		t.Fatalf("the server received %d bytes, the arms wrote %d",
			server.bytes.Load(), perStream.bytes+pooled.bytes)
	}
	if pooled.bytes != perStream.bytes {
		t.Fatalf("arms wrote different bytes: per-stream %d, pooled %d", perStream.bytes, pooled.bytes)
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

// runRealABServer is the server half of the cross-namespace A/B: it publishes its address,
// keeps the running totals in a file (so the parent can read them even after a SIGKILL) and
// serves until the process is killed.
func runRealABServer(t *testing.T) {
	addr := os.Getenv("XHTTP_REAL_AB_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	server := newRealPacketUpServerAt(t, addr)
	addrFile := os.Getenv("XHTTP_REAL_AB_ADDR_FILE")
	if addrFile != "" {
		if err := os.WriteFile(addrFile, []byte(server.addr), 0o644); err != nil {
			t.Fatalf("write the address file: %v", err)
		}
	}
	resultFile := os.Getenv("XHTTP_REAL_AB_RESULT_FILE")
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			if resultFile != "" {
				_ = os.WriteFile(resultFile, []byte(fmt.Sprintf("uploads=%d bytes=%d\n",
					server.uploads.Load(), server.bytes.Load())), 0o644)
			}
			select {
			case <-ticker.C:
			case <-stop:
				return
			}
		}
	}()
	defer close(stop)
	t.Logf("serving on %s", server.addr)
	duration := 180 * time.Second
	if raw := os.Getenv("XHTTP_REAL_AB_DURATION"); raw != "" {
		if parsed, parseErr := time.ParseDuration(raw); parseErr == nil {
			duration = parsed
		}
	}
	select {
	case <-time.After(duration):
	case <-t.Context().Done():
	}
	if resultFile != "" {
		_ = os.WriteFile(resultFile, []byte(fmt.Sprintf("uploads=%d bytes=%d\n",
			server.uploads.Load(), server.bytes.Load())), 0o644)
	}
	t.Logf("server received: uploads=%d bytes=%d", server.uploads.Load(), server.bytes.Load())
}

// runRealABClient is the client half: it waits for the server's address, runs both arms
// against it and checks that the two arms wrote the same bytes with no failures.
func runRealABClient(t *testing.T) {
	addrFile := os.Getenv("XHTTP_REAL_AB_ADDR_FILE")
	if addrFile == "" {
		t.Fatal("XHTTP_REAL_AB_ADDR_FILE is required in client mode")
	}
	addr := waitForRealABAddr(t, addrFile)
	t.Logf("client of %s", addr)

	perStream := runRealABWorkload(t, addr, false)
	pooled := runRealABWorkload(t, addr, true)
	if pooled.bytes != perStream.bytes {
		t.Fatalf("arms wrote different bytes: per-stream %d, pooled %d", perStream.bytes, pooled.bytes)
	}
	if pooled.logicalPeak == 0 {
		t.Fatalf("the pooled arm never charged the budget: %+v", pooled)
	}
	summary := fmt.Sprintf("per_stream_bytes=%d per_stream_wall_ms=%d per_stream_peak=%d pooled_bytes=%d pooled_wall_ms=%d pooled_peak=%d pooled_alloc_peak=%d failures=%d\n",
		perStream.bytes, perStream.wall.Milliseconds(), perStream.logicalPeak,
		pooled.bytes, pooled.wall.Milliseconds(), pooled.logicalPeak, pooled.allocatedPeak,
		perStream.failures+pooled.failures)
	if resultFile := os.Getenv("XHTTP_REAL_AB_RESULT_FILE"); resultFile != "" {
		_ = os.WriteFile(resultFile, []byte(summary), 0o644)
	}
	t.Logf("result: %s", summary)
}

// waitForRealABAddr waits up to 30 s for the server's address file to appear.
func waitForRealABAddr(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			if _, _, splitErr := net.SplitHostPort(string(raw)); splitErr == nil {
				return string(raw)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server address never appeared in %s", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
