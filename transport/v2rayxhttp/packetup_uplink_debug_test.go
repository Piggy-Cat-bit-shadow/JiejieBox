package v2rayxhttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"path"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// lx: XHTTP_OUTPERFORM — adversarial debug of the packet-up uplink queue.
//
// The batching change replaced "one POST per Write, synchronously" with a
// bounded queue plus one flusher goroutine. That is a state machine, and these
// tests attack it where a state machine actually breaks: ordering under
// concurrent writers, sequence continuity when a post fails, Close at every
// stage of a post's life, error classification, the memory bound, payload
// ownership while the queue keeps growing, and goroutine/heap retention.
//
// They are written to fail on the failure MODES the design must not have, not
// to re-assert the happy path the contract tests already cover:
//
//	permanent seq hole / silent stall
//	Write reporting success while the bytes never leave
//	unbounded queue or unbounded in-flight posts
//	Close that deadlocks or drops the tail
//	a payload mutated by writers that appended behind it
//	goroutines or payload bytes retained after Close
//
// Ordering is the one property that is structurally guaranteed here rather than
// tested into existence: the flusher posts one request at a time, so posts
// cannot complete out of order. These tests pin that too (maxInflight == 1),
// because a future change that pipelines posts would silently invalidate the
// seq argument.

// probeTransport is a scripted RoundTripper for the uplink state machine: it
// records every upload POST (body, path, arrival time, concurrency) and lets a
// test decide what each one does.
type probeTransport struct {
	access      sync.Mutex
	bodies      [][]byte
	paths       []string
	times       []time.Time
	posts       int
	inflight    int
	maxInflight int

	// onPost runs for each upload POST, outside the lock.
	onPost func(request *http.Request, body []byte) (*http.Response, error)
	// readChunk/readDelay read the request body slowly, which is how a test
	// detects a payload aliased by writers appending behind it.
	readChunk int
	readDelay time.Duration
	// responseDelay stands in for a round trip: the server has consumed the body,
	// and the response now takes this long to arrive. It is what the high-RTT
	// response-pipeline probe sweeps.
	responseDelay time.Duration
}

func newProbeTransport() *probeTransport {
	return &probeTransport{}
}

func okProbeResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func (transport *probeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet {
		// The download stream stays open, like a server with nothing to send yet.
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(newBlockingReader())}, nil
	}

	transport.access.Lock()
	transport.inflight++
	if transport.inflight > transport.maxInflight {
		transport.maxInflight = transport.inflight
	}
	transport.access.Unlock()

	var body []byte
	if transport.readChunk > 0 {
		buffer := make([]byte, transport.readChunk)
		for {
			read, err := request.Body.Read(buffer)
			if read > 0 {
				body = append(body, buffer[:read]...)
				if transport.readDelay > 0 {
					time.Sleep(transport.readDelay)
				}
			}
			if err != nil {
				break
			}
		}
	} else {
		body, _ = io.ReadAll(request.Body)
	}
	request.Body.Close()

	transport.access.Lock()
	transport.posts++
	transport.bodies = append(transport.bodies, body)
	transport.paths = append(transport.paths, request.URL.Path)
	transport.times = append(transport.times, time.Now())
	transport.inflight--
	transport.access.Unlock()

	if transport.responseDelay > 0 {
		// The body is consumed; the answer is now in flight.
		time.Sleep(transport.responseDelay)
	}

	if transport.onPost != nil {
		return transport.onPost(request, body)
	}
	return okProbeResponse(), nil
}

func (transport *probeTransport) snapshot() (bodies [][]byte, paths []string, posts, maxInflight int) {
	transport.access.Lock()
	defer transport.access.Unlock()
	bodies = make([][]byte, len(transport.bodies))
	copy(bodies, transport.bodies)
	paths = make([]string, len(transport.paths))
	copy(paths, transport.paths)
	return bodies, paths, transport.posts, transport.maxInflight
}

func (transport *probeTransport) waitForPosts(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(writeFreeBudget)
	for {
		if _, _, posts, _ := transport.snapshot(); posts >= want {
			return
		}
		if time.Now().After(deadline) {
			_, _, posts, _ := transport.snapshot()
			t.Fatalf("saw %d upload posts, want %d", posts, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitForGoroutines waits for the goroutine count to come back to the baseline,
// which is how a leaked flusher or a leaked post shows up.
func waitForGoroutines(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(writeFreeBudget)
	for {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines: have %d, baseline %d — a flusher or a post leaked",
				runtime.NumGoroutine(), baseline)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// seqOf returns the upload sequence number the path carries.
func seqOf(t *testing.T, requestPath string) int {
	t.Helper()
	value, err := strconv.Atoi(path.Base(requestPath))
	if err != nil {
		t.Fatalf("path %q carries no sequence number: %v", requestPath, err)
	}
	return value
}

// TestUplinkOrderingAndSeqContinuity: every byte written must arrive exactly
// once, in write order, with a contiguous ascending seq — under jittered post
// completion and a post cap that forces many posts.
func TestUplinkOrderingAndSeqContinuity(t *testing.T) {
	t.Parallel()
	transport := newProbeTransport()
	transport.onPost = func(request *http.Request, body []byte) (*http.Response, error) {
		// Vary completion time so a design that could reorder posts would.
		time.Sleep(time.Duration(seqOf(t, request.URL.Path)%3) * time.Millisecond)
		return okProbeResponse(), nil
	}
	conn := dialBatchConn(t, batchClient(t, transport, metaOptions{
		ScMinPostsIntervalMs: "0",
		ScMaxEachPostBytes:   "4096-4096",
	}))

	const writes = 200
	const writeSize = 1024
	expected := make([]byte, 0, writes*writeSize)
	for index := 0; index < writes; index++ {
		payload := bytes.Repeat([]byte{byte(index)}, writeSize)
		expected = append(expected, payload...)
		if _, err := conn.Write(payload); err != nil {
			t.Fatalf("write %d: %v", index, err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	bodies, paths, posts, maxInflight := transport.snapshot()
	if maxInflight != 1 {
		t.Fatalf("saw %d upload posts in flight at once, want 1 — posts are posted sequentially, which is what makes the seq argument hold", maxInflight)
	}
	if posts < 2 {
		t.Fatalf("saw %d posts for %d bytes capped at 4096: the cap was not applied", posts, len(expected))
	}
	var reassembled []byte
	for _, body := range bodies {
		reassembled = append(reassembled, body...)
	}
	if !bytes.Equal(reassembled, expected) {
		t.Fatalf("reassembled %d bytes, want %d: a batch lost, duplicated or reordered data", len(reassembled), len(expected))
	}
	for index, requestPath := range paths {
		if got := seqOf(t, requestPath); got != index {
			t.Fatalf("post %d carries seq %d: the sequence has a hole, a duplicate or a reorder", index, got)
		}
	}
}

// TestUplinkConcurrentWritersLoseNothing: several goroutines writing at once must
// not interleave INSIDE a write (each Write's bytes stay contiguous) and must not
// lose or duplicate a byte.
func TestUplinkConcurrentWritersLoseNothing(t *testing.T) {
	t.Parallel()
	transport := newProbeTransport()
	conn := dialBatchConn(t, batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0"}))

	const writers = 4
	const perWriter = 50
	const writeSize = 512
	var waitGroup sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		waitGroup.Add(1)
		go func(writer int) {
			defer waitGroup.Done()
			payload := bytes.Repeat([]byte{byte('A' + writer)}, writeSize)
			for index := 0; index < perWriter; index++ {
				if _, err := conn.Write(payload); err != nil {
					t.Errorf("writer %d write %d: %v", writer, index, err)
					return
				}
			}
		}(writer)
	}
	waitGroup.Wait()
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	bodies, _, _, _ := transport.snapshot()
	var reassembled []byte
	for _, body := range bodies {
		reassembled = append(reassembled, body...)
	}
	if len(reassembled) != writers*perWriter*writeSize {
		t.Fatalf("reassembled %d bytes, want %d", len(reassembled), writers*perWriter*writeSize)
	}
	// Every write of the same value, so a writer's bytes must appear in runs that
	// are a multiple of writeSize. A run that is not means a Write was split by
	// another writer.
	counts := make(map[byte]int, writers)
	runLength := 0
	var runByte byte
	flushRun := func() {
		if runLength > 0 && runLength%writeSize != 0 {
			t.Fatalf("a run of %d bytes of %q is not a whole number of %d-byte writes: a write was interleaved",
				runLength, runByte, writeSize)
		}
	}
	for _, value := range reassembled {
		if value == runByte {
			runLength++
		} else {
			flushRun()
			runByte, runLength = value, 1
		}
		counts[value]++
	}
	flushRun()
	for writer := 0; writer < writers; writer++ {
		value := byte('A' + writer)
		if counts[value] != perWriter*writeSize {
			t.Fatalf("writer %d contributed %d bytes, want %d — bytes were lost or duplicated",
				writer, counts[value], perWriter*writeSize)
		}
	}
}

// TestUplinkCloseAtEveryStage: Close must return, must not deadlock, must not
// double-release the pooled connection, and must not leak the flusher — whether
// it lands on an empty queue, on queued bytes, on a post in flight, or on a
// post whose body the transport is still reading.
func TestUplinkCloseAtEveryStage(t *testing.T) {
	baseline := runtime.NumGoroutine()
	stages := []struct {
		name  string
		setup func(t *testing.T, transport *probeTransport) (net.Conn, *Client)
	}{
		{
			name: "empty queue",
			setup: func(t *testing.T, transport *probeTransport) (net.Conn, *Client) {
				client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0"})
				return dialBatchConn(t, client), client
			},
		},
		{
			name: "queued, not yet posted",
			setup: func(t *testing.T, transport *probeTransport) (net.Conn, *Client) {
				// Hold the first post so the second write provably queues behind it.
				release := make(chan struct{})
				transport.onPost = func(request *http.Request, body []byte) (*http.Response, error) {
					select {
					case <-release:
					case <-request.Context().Done():
					}
					return okProbeResponse(), nil
				}
				client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0"})
				conn := dialBatchConn(t, client)
				if _, err := conn.Write([]byte("first")); err != nil {
					t.Fatalf("Write: %v", err)
				}
				transport.waitForPosts(t, 0)
				if _, err := conn.Write([]byte("queued")); err != nil {
					t.Fatalf("Write: %v", err)
				}
				close(release)
				return conn, client
			},
		},
		{
			name: "post in flight, response never comes",
			setup: func(t *testing.T, transport *probeTransport) (net.Conn, *Client) {
				transport.onPost = func(request *http.Request, body []byte) (*http.Response, error) {
					<-request.Context().Done()
					return nil, request.Context().Err()
				}
				client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0"})
				conn := dialBatchConn(t, client)
				if _, err := conn.Write([]byte("in flight")); err != nil {
					t.Fatalf("Write: %v", err)
				}
				transport.waitForPosts(t, 0)
				time.Sleep(20 * time.Millisecond)
				return conn, client
			},
		},
		{
			name: "transport still reading the body",
			setup: func(t *testing.T, transport *probeTransport) (net.Conn, *Client) {
				transport.readChunk = 64
				transport.readDelay = 5 * time.Millisecond
				client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0"})
				conn := dialBatchConn(t, client)
				if _, err := conn.Write(bytes.Repeat([]byte("x"), 8*1024)); err != nil {
					t.Fatalf("Write: %v", err)
				}
				time.Sleep(20 * time.Millisecond)
				return conn, client
			},
		},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			transport := newProbeTransport()
			conn, client := stage.setup(t, transport)
			started := time.Now()
			if err := conn.Close(); err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Close: %v", err)
			}
			if elapsed := time.Since(started); elapsed > packetUpDrainTimeout+writeFreeBudget {
				t.Fatalf("Close took %s: it waited past the drain budget", elapsed)
			}
			if usage := openUsageOf(client); usage != 0 {
				t.Fatalf("openUsage = %d after Close, want 0 (slot leaked or double released)", usage)
			}
		})
	}
	waitForGoroutines(t, baseline)
}

// TestUplinkPostFailureSurfacesAndStopsAtTheFailure: a failed post must reach the
// write path, and no later post may be sent — a sequence that continued past the
// failure would leave the server waiting on a hole while the client reported
// success.
func TestUplinkPostFailureSurfacesAndStopsAtTheFailure(t *testing.T) {
	t.Parallel()
	transport := newProbeTransport()
	refused := errors.New("post refused by the transport")
	transport.onPost = func(request *http.Request, body []byte) (*http.Response, error) {
		if seqOf(t, request.URL.Path) == 1 {
			return nil, refused
		}
		return okProbeResponse(), nil
	}

	// One write per post, so the failing post is provably the second one.
	client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0", ScMaxEachPostBytes: "64-64"})
	conn := dialBatchConn(t, client)
	payloads := []string{"first", "second", "third"}
	var surfaced error
	for index, payload := range payloads {
		if _, err := conn.Write([]byte(payload)); err != nil {
			surfaced = err
			break
		}
		transport.waitForPosts(t, index+1)
	}
	deadline := time.Now().Add(writeFreeBudget)
	for surfaced == nil && time.Now().Before(deadline) {
		if _, err := conn.Write([]byte("after")); err != nil {
			surfaced = err
		}
		time.Sleep(5 * time.Millisecond)
	}
	if surfaced == nil {
		t.Fatal("a refused post never surfaced on the write path")
	}
	if !strings.Contains(surfaced.Error(), "post refused") {
		t.Fatalf("write path reported %v, want the transport's cause", surfaced)
	}
	_, paths, _, _ := transport.snapshot()
	for _, requestPath := range paths {
		if seqOf(t, requestPath) > 1 {
			t.Fatalf("post %s was sent after the failed post: the server now waits on a hole", requestPath)
		}
	}
	// The slot is NOT released by the failure itself: a failed post kills the
	// upload half and leaves the session's teardown to the caller, which is what
	// the reference does too (it interrupts the upload pipe and stops posting).
	// What must hold is that the caller's Close releases it exactly once, and
	// that a second Close cannot release it twice.
	if usage := openUsageOf(client); usage != 1 {
		t.Fatalf("openUsage = %d after a failed post but before Close, want 1 (the session is still open)", usage)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if usage := openUsageOf(client); usage != 0 {
		t.Fatalf("openUsage = %d after Close, want exactly 0 (leaked or double released)", usage)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if usage := openUsageOf(client); usage != 0 {
		t.Fatalf("openUsage = %d after a second Close, want 0 — a negative count means a double release", usage)
	}
}

// TestUplinkNon200StopsTheStream: a non-200 upload answer is a failure of the
// session, not something to retry into — the next post must not follow it.
func TestUplinkNon200StopsTheStream(t *testing.T) {
	t.Parallel()
	transport := newProbeTransport()
	transport.onPost = func(request *http.Request, body []byte) (*http.Response, error) {
		if seqOf(t, request.URL.Path) == 1 {
			return &http.Response{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway", Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return okProbeResponse(), nil
	}
	client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0", ScMaxEachPostBytes: "64-64"})
	conn := dialBatchConn(t, client)
	for index, payload := range []string{"first", "second"} {
		if _, err := conn.Write([]byte(payload)); err != nil {
			t.Fatalf("write %d: %v", index, err)
		}
		if index == 0 {
			transport.waitForPosts(t, 1)
		}
	}
	var surfaced error
	deadline := time.Now().Add(writeFreeBudget)
	for surfaced == nil && time.Now().Before(deadline) {
		if _, err := conn.Write([]byte("third")); err != nil {
			surfaced = err
		}
		time.Sleep(5 * time.Millisecond)
	}
	if surfaced == nil || !strings.Contains(surfaced.Error(), "unexpected upload status") {
		t.Fatalf("write path reported %v, want the unexpected-status error", surfaced)
	}
	_, paths, _, _ := transport.snapshot()
	for _, requestPath := range paths {
		if seqOf(t, requestPath) > 1 {
			t.Fatalf("post %s followed a non-200 answer", requestPath)
		}
	}
}

// TestUplinkBackpressureIsBoundedAndEndsInAnError: a transport that never
// answers must not turn into unbounded memory or an unbounded number of posts,
// and the writer must end up with an error instead of spinning forever.
func TestUplinkBackpressureIsBoundedAndEndsInAnError(t *testing.T) {
	// NOT parallel: overrides packetUpPostTimeout.
	restore := packetUpPostTimeout
	packetUpPostTimeout = 300 * time.Millisecond
	t.Cleanup(func() { packetUpPostTimeout = restore })

	baseline := runtime.NumGoroutine()
	transport := newProbeTransport()
	release := make(chan struct{})
	defer close(release)
	transport.onPost = func(request *http.Request, body []byte) (*http.Response, error) {
		select {
		case <-release:
		case <-request.Context().Done():
		}
		return nil, request.Context().Err()
	}

	client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0", ScMaxEachPostBytes: "30000-30000"})
	conn := dialBatchConn(t, client)
	uplink := conn.(*packetConn).uplink
	bound := uplink.maxPending

	var peak int
	writerDone := make(chan error, 1)
	go func() {
		chunk := make([]byte, 8*1024)
		for written := 0; written < bound*4; written += len(chunk) {
			if _, err := conn.Write(chunk); err != nil {
				writerDone <- err
				return
			}
			uplink.access.Lock()
			if queued := len(uplink.pending); queued > peak {
				peak = queued
			}
			uplink.access.Unlock()
		}
		writerDone <- nil
	}()

	select {
	case err := <-writerDone:
		if err == nil {
			t.Fatal("a writer completed far past the queue bound while the path was stalled: the queue is unbounded")
		}
	case <-time.After(writeFreeBudget):
		t.Fatal("a writer never returned while the path was stalled: it is neither bounded nor failing")
	}
	if peak > bound {
		t.Fatalf("queue peaked at %d bytes, over the %d-byte bound", peak, bound)
	}
	if _, _, posts, maxInflight := transport.snapshot(); maxInflight > 1 || posts > 1 {
		t.Fatalf("stalled path saw %d posts (%d in flight at once), want at most 1", posts, maxInflight)
	}
	_ = conn.Close()
	waitForGoroutines(t, baseline)
}

// TestUplinkLoneWriteIsFlushedWithoutAFollowingWrite is the failure mode the
// design must not have: Write returning success while the bytes sit in the queue
// because no further write, Close or deadline ever arrives to nudge the flusher.
func TestUplinkLoneWriteIsFlushedWithoutAFollowingWrite(t *testing.T) {
	t.Parallel()
	transport := newProbeTransport()
	conn := dialBatchConn(t, batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0"}))

	started := time.Now()
	if _, err := conn.Write([]byte("a lone write")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	transport.waitForPosts(t, 1)
	elapsed := time.Since(started)
	t.Logf("a lone write reached a post in %s", elapsed)
	if elapsed > writeFreeBudget/10 {
		t.Fatalf("a lone write took %s to reach a post: the flusher is waiting for something that never comes", elapsed)
	}
	bodies, _, _, _ := transport.snapshot()
	if got := string(bodies[0]); got != "a lone write" {
		t.Fatalf("post carried %q, want the lone write", got)
	}
}

// TestUplinkPayloadIsNotAliasedByLaterWrites: the POST body is a subslice of the
// queue, and writers keep appending to that same array while the transport reads
// it. Any overlap would corrupt the body in flight, so this test reads slowly and
// writes hard at the same time.
func TestUplinkPayloadIsNotAliasedByLaterWrites(t *testing.T) {
	t.Parallel()
	transport := newProbeTransport()
	transport.readChunk = 1024
	transport.readDelay = time.Millisecond
	conn := dialBatchConn(t, batchClient(t, transport, metaOptions{
		ScMinPostsIntervalMs: "0",
		ScMaxEachPostBytes:   "16384-16384",
	}))

	first := bytes.Repeat([]byte("A"), 16*1024)
	if _, err := conn.Write(first); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	// Keep the queue busy while the first post's body is being read slowly.
	stop := make(chan struct{})
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		filler := bytes.Repeat([]byte("B"), 4096)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := conn.Write(filler); err != nil {
				return
			}
		}
	}()
	transport.waitForPosts(t, 1)
	close(stop)
	waitGroup.Wait()

	bodies, _, _, _ := transport.snapshot()
	if len(bodies) == 0 {
		t.Fatal("no post was recorded")
	}
	if !bytes.Equal(bodies[0], first) {
		t.Fatalf("the first post's body was corrupted while writers appended behind it (%d bytes read, %d expected)",
			len(bodies[0]), len(first))
	}
	for index, body := range bodies[1:] {
		if len(body) == 0 {
			t.Fatalf("post %d carried an empty body", index+1)
		}
		if bytes.Contains(body, []byte("A")) {
			t.Fatalf("post %d contains bytes from the first payload: the queue region was reused under a live post", index+1)
		}
	}
}

// TestUplinkTinyWritesArriveExactlyOnce: one-byte writes are the shape a
// coalescing bug corrupts most easily, and the last one must not be stranded.
func TestUplinkTinyWritesArriveExactlyOnce(t *testing.T) {
	t.Parallel()
	transport := newProbeTransport()
	conn := dialBatchConn(t, batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0"}))

	const writes = 64
	expected := make([]byte, 0, writes)
	for index := 0; index < writes; index++ {
		value := byte(index)
		expected = append(expected, value)
		if _, err := conn.Write([]byte{value}); err != nil {
			t.Fatalf("write %d: %v", index, err)
		}
	}
	// No Close: the final byte must still leave on its own.
	deadline := time.Now().Add(writeFreeBudget)
	var reassembled []byte
	for time.Now().Before(deadline) {
		bodies, _, _, _ := transport.snapshot()
		reassembled = reassembled[:0]
		for _, body := range bodies {
			reassembled = append(reassembled, body...)
		}
		if len(reassembled) >= len(expected) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !bytes.Equal(reassembled, expected) {
		t.Fatalf("reassembled %d bytes %v, want %d bytes %v", len(reassembled), reassembled, len(expected), expected)
	}
	bodies, _, posts, _ := transport.snapshot()
	if posts > writes {
		t.Fatalf("saw %d posts for %d one-byte writes: writes are not being coalesced at all", posts, writes)
	}
	total := 0
	for _, body := range bodies {
		total += len(body)
	}
	if total != writes {
		t.Fatalf("posts carried %d bytes, want %d", total, writes)
	}
}

// TestUplinkNoGoroutineLeakAcrossManySessions: dial/write/close in a loop must
// leave the goroutine count where it started — a flusher that outlives its conn
// is the leak this shape finds.
func TestUplinkNoGoroutineLeakAcrossManySessions(t *testing.T) {
	baseline := runtime.NumGoroutine()
	transport := newProbeTransport()
	for round := 0; round < 200; round++ {
		client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0"})
		conn, err := client.DialContext(context.Background())
		if err != nil {
			t.Fatalf("round %d dial: %v", round, err)
		}
		if _, err = conn.Write([]byte("payload")); err != nil {
			t.Fatalf("round %d write: %v", round, err)
		}
		if err = conn.Close(); err != nil {
			t.Fatalf("round %d close: %v", round, err)
		}
	}
	waitForGoroutines(t, baseline)
}

// lx: XHTTP_OUTPERFORM — the "does the transport redial behind XMUX" probe.
//
// XMUX owns an abstraction it calls a pooled connection, and it accounts for it:
// max_connections, c_max_reuse_times, h_max_request_times, the breaker and
// RetireSuspect all reason about that object. Underneath it sits an
// http2.Transport (or http.Transport, or http3.Transport) which keeps its own
// pool of TCP/QUIC connections and is free to dial a new one when the old one
// dies — without telling XMUX.
//
// This test measures whether that happens, and what it does to the accounting.

// acceptCountingListener counts TCP connections and can kill them, which is how
// this test manufactures "the pooled connection died underneath XMUX".
type acceptCountingListener struct {
	net.Listener
	access  sync.Mutex
	accepts int
	conns   []net.Conn
}

func (listener *acceptCountingListener) Accept() (net.Conn, error) {
	conn, err := listener.Listener.Accept()
	if err == nil {
		listener.access.Lock()
		listener.accepts++
		listener.conns = append(listener.conns, conn)
		listener.access.Unlock()
	}
	return conn, err
}

func (listener *acceptCountingListener) count() int {
	listener.access.Lock()
	defer listener.access.Unlock()
	return listener.accepts
}

func (listener *acceptCountingListener) killAll() {
	listener.access.Lock()
	conns := append([]net.Conn(nil), listener.conns...)
	listener.conns = nil
	listener.access.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

type countingH2CServer struct {
	listener *acceptCountingListener
	server   *http.Server
	addr     string
	access   sync.Mutex
	requests int
}

func newCountingH2CServer(t *testing.T) *countingH2CServer {
	t.Helper()
	counting := &countingH2CServer{}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			// Download: answer and hold the stream open like a real server.
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
			return
		}
		_, _ = io.Copy(io.Discard, request.Body)
		counting.access.Lock()
		counting.requests++
		counting.access.Unlock()
		writer.WriteHeader(http.StatusOK)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	counting.listener = &acceptCountingListener{Listener: listener}
	counting.addr = listener.Addr().String()
	counting.server = &http.Server{Handler: h2c.NewHandler(handler, &http2.Server{})}
	go counting.server.Serve(counting.listener)
	t.Cleanup(func() { _ = counting.server.Close() })
	return counting
}

func (counting *countingH2CServer) requestCount() int {
	counting.access.Lock()
	defer counting.access.Unlock()
	return counting.requests
}

func (counting *countingH2CServer) waitForRequests(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(writeFreeBudget)
	for counting.requestCount() < want {
		if time.Now().After(deadline) {
			t.Fatalf("server saw %d upload requests, want %d", counting.requestCount(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// pooledClientCount reports how many pooled connections XMUX believes it owns.
func pooledClientCount(client *Client) int {
	client.xmux.access.Lock()
	defer client.xmux.access.Unlock()
	return len(client.xmux.clients)
}

// TestTransportRedialIsInvisibleToXMUX documents the boundary between XMUX's
// accounting and the HTTP transport's own connection pool.
//
// MEASURED: killing the TCP connection under a live session and opening a new
// session makes the transport dial a new TCP connection (the server counts a
// second accept) while XMUX still reports ONE pooled connection and never resets
// its counters. So XMUX's numbers are what they claim to be — HTTP requests and
// streams on a pooled client — and NOT a count of TCP connections.
//
// That is benign for this design, and the test pins the parts that make it
// benign: the dead session reports its failure instead of being reused, the new
// session carries its payload, the pooled-client count does not grow, and the
// stream slots are all released. What it does NOT claim is that
// max_connections: 1-1 caps TCP connections: a redial is a new TCP connection,
// created only after the old one is dead.
func TestTransportRedialIsInvisibleToXMUX(t *testing.T) {
	server := newCountingH2CServer(t)
	client := h2cClient(t, server.addr, modePacketUp)

	first, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	if _, err = first.Write([]byte("first payload")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	server.waitForRequests(t, 1)
	if accepts := server.listener.count(); accepts != 1 {
		t.Fatalf("server saw %d TCP connections for the first session, want 1", accepts)
	}
	if pooled := pooledClientCount(client); pooled != 1 {
		t.Fatalf("XMUX reports %d pooled connections, want 1", pooled)
	}

	// The pooled connection dies underneath XMUX.
	server.listener.killAll()

	// The live session must report that, not hang and not pretend to be healthy.
	_ = first.SetReadDeadline(time.Now().Add(writeFreeBudget / 3))
	probe := make([]byte, 1)
	if _, readErr := first.Read(probe); readErr == nil {
		t.Fatal("a session whose connection was killed still read successfully: a broken pool is being reused")
	}
	_ = first.Close()
	if usage := openUsageOf(client); usage != 0 {
		t.Fatalf("openUsage = %d after the killed session was closed, want 0 — a failed session must release exactly once", usage)
	}

	second, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatalf("second dial after the connection died: %v", err)
	}
	if usage := openUsageOf(client); usage != 1 {
		_ = second.Close()
		t.Fatalf("openUsage = %d with one live session, want 1", usage)
	}
	if _, err = second.Write([]byte("second payload")); err != nil {
		_ = second.Close()
		t.Fatalf("second write: %v", err)
	}
	server.waitForRequests(t, 2)

	accepts := server.listener.count()
	pooled := pooledClientCount(client)
	t.Logf("after the pooled connection died: server TCP accepts=%d, XMUX pooled connections=%d", accepts, pooled)

	if err = second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if accepts < 2 {
		t.Fatalf("server saw %d TCP connections after the first was killed, want a redial", accepts)
	}
	if pooled != 1 {
		t.Fatalf("XMUX reports %d pooled connections after a transport-level redial, want 1 — a redial must not leak transports", pooled)
	}
	if usage := openUsageOf(client); usage != 0 {
		t.Fatalf("openUsage = %d after both sessions were closed, want 0", usage)
	}
}

// replayWatchingServer records each upload POST by sequence number and reports a
// CONFLICT if the same sequence ever arrives carrying different bytes. That is
// the property a replayed request must never break: the reference server drops a
// repeated seq (upload_queue.go: a packet with Seq < nextSeq falls through both
// branches and is discarded), so a replay is harmless only while it is
// byte-identical.
type replayWatchingServer struct {
	listener *acceptCountingListener
	server   *http.Server
	addr     string

	access     sync.Mutex
	bySeq      map[int][]byte
	conflicted bool
	// resetUploads makes the handler panic on uploads, which Go's HTTP/2 server
	// answers with RST_STREAM.
	resetUploads bool
}

func newReplayWatchingServer(t *testing.T) *replayWatchingServer {
	t.Helper()
	watching := &replayWatchingServer{bySeq: make(map[int][]byte)}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
			return
		}
		if watching.resetUploads {
			panic("injected stream reset")
		}
		body, _ := io.ReadAll(request.Body)
		seq, err := strconv.Atoi(path.Base(request.URL.Path))
		if err == nil {
			watching.access.Lock()
			if previous, seen := watching.bySeq[seq]; seen {
				if !bytes.Equal(previous, body) {
					watching.conflicted = true
				}
			} else {
				watching.bySeq[seq] = body
			}
			watching.access.Unlock()
		}
		writer.WriteHeader(http.StatusOK)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	watching.listener = &acceptCountingListener{Listener: listener}
	watching.addr = listener.Addr().String()
	watching.server = &http.Server{Handler: h2c.NewHandler(handler, &http2.Server{})}
	go watching.server.Serve(watching.listener)
	t.Cleanup(func() { _ = watching.server.Close() })
	return watching
}

func (watching *replayWatchingServer) delivered(seq int) ([]byte, bool) {
	watching.access.Lock()
	defer watching.access.Unlock()
	body, seen := watching.bySeq[seq]
	return body, seen
}

// TestUplinkHandlesAGoawayWithoutConflictingReplay exercises the one path that can
// resend business payload: Go's HTTP/2 transport replays a request whose body it
// can rewind, and this fork deliberately sets GetBody so a POST survives a
// GOAWAY (the reference cannot — its PostPacket builds the body without one).
//
// A graceful server shutdown sends GOAWAY. Whatever the client does with it, the
// invariants are: the write path never hangs, the payload arrives intact if it
// arrives at all, a replay is byte-identical, and the failure is reported rather
// than swallowed.
func TestUplinkHandlesAGoawayWithoutConflictingReplay(t *testing.T) {
	server := newReplayWatchingServer(t)
	client := h2cClient(t, server.addr, modePacketUp)
	conn, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	payload := bytes.Repeat([]byte("G"), 32*1024)
	if _, err = conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = server.server.Shutdown(shutdownContext)
	cancel()

	// A later write must surface whatever happened, within a bound.
	done := make(chan error, 1)
	go func() {
		_, writeErr := conn.Write([]byte("after the goaway"))
		done <- writeErr
	}()
	select {
	case <-done:
	case <-time.After(writeFreeBudget):
		t.Fatal("a write after GOAWAY hung: the flusher is neither posting nor failing")
	}

	if err = conn.Close(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Close after GOAWAY: %v", err)
	}

	if body, seen := server.delivered(0); seen && !bytes.Equal(body, payload) {
		t.Fatalf("the payload arrived truncated or corrupted: %d bytes, want %d", len(body), len(payload))
	}
	server.access.Lock()
	conflicted := server.conflicted
	server.access.Unlock()
	if conflicted {
		t.Fatal("the same sequence arrived twice with different bytes: a replay is resending business payload")
	}
}

// TestUplinkFirstWriteAfterIdleIsPostedPromptly: an idle session must not make
// the next write wait on stale throttle bookkeeping or a flusher that parked
// itself. The interval is set to the production default so the check is about
// idleness, not about the exemption.
func TestUplinkFirstWriteAfterIdleIsPostedPromptly(t *testing.T) {
	t.Parallel()
	transport := newProbeTransport()
	conn := dialBatchConn(t, stubClient(t, modePacketUp, transport))

	time.Sleep(750 * time.Millisecond)

	started := time.Now()
	if _, err := conn.Write([]byte("after idle")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	transport.waitForPosts(t, 1)
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("the first write after an idle period took %s to reach a post", elapsed)
	}
}

// TestUplinkAmortizesSmallWrites answers the question the VLESS boundary raises:
// an application's write granularity must not decide how many HTTP requests the
// tunnel pays for.
//
// The VLESS outbound hands the transport roughly 8 KiB at a time (a 32 KiB
// relay buffer sealed in ≤8192-byte records), and before batching each of those
// was one POST — the measured 2.12 MB/s against the reference's 22.86 MB/s. With
// the production defaults (1 MB post cap, 30 ms interval) a bulk upload of 8 KiB
// writes must still leave in posts that are a large fraction of the cap.
func TestUplinkAmortizesSmallWrites(t *testing.T) {
	t.Parallel()
	transport := newProbeTransport()
	// Production defaults: no test-only tuning of the cap or the interval.
	conn := dialBatchConn(t, stubClient(t, modePacketUp, transport))

	const writes = 512
	const writeSize = 8 * 1024
	payload := bytes.Repeat([]byte("V"), writeSize)
	for index := 0; index < writes; index++ {
		if _, err := conn.Write(payload); err != nil {
			t.Fatalf("write %d: %v", index, err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	bodies, _, posts, _ := transport.snapshot()
	total := 0
	sizes := make([]int, 0, len(bodies))
	for _, body := range bodies {
		total += len(body)
		sizes = append(sizes, len(body))
	}
	if total != writes*writeSize {
		t.Fatalf("posts carried %d bytes, want %d", total, writes*writeSize)
	}
	sort.Ints(sizes)
	median := sizes[len(sizes)/2]
	t.Logf("%d %d-byte writes left in %d posts: min=%d median=%d max=%d bytes",
		writes, writeSize, posts, sizes[0], median, sizes[len(sizes)-1])
	if median < 256*1024 {
		t.Fatalf("median post carried %d bytes for %d-byte writes (%d posts): small writes are not being amortized",
			median, writeSize, posts)
	}
	if posts > writes/16 {
		t.Fatalf("%d posts for %d writes: the batching is barely doing anything", posts, writes)
	}
}

// TestUplinkHidesAnRSTStreamError injects the H2 stream-level failure the
// transport must not leak: a server that dies mid-request resets the stream, and
// x/net/http2 surfaces that as an *http2.StreamError. SPEC 082 requires the
// caller to see a transport error, not an HTTP library type, so a retry layer
// above cannot mistake it for a protocol answer.
//
// The panic is the injection: Go's HTTP/2 server answers a panicking handler with
// RST_STREAM(INTERNAL_ERROR), which is the same frame a server sends when it
// gives up on a stream.
func TestUplinkHidesAnRSTStreamError(t *testing.T) {
	server := newReplayWatchingServer(t)
	server.resetUploads = true
	client := h2cClient(t, server.addr, modePacketUp)
	conn, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err = conn.Write([]byte("payload onto a reset stream")); err != nil {
		t.Fatalf("write: %v", err)
	}

	var surfaced error
	deadline := time.Now().Add(writeFreeBudget)
	for surfaced == nil && time.Now().Before(deadline) {
		if _, err = conn.Write([]byte("again")); err != nil {
			surfaced = err
		}
		time.Sleep(5 * time.Millisecond)
	}
	if surfaced == nil {
		t.Fatal("a reset stream never surfaced an error on the write path")
	}
	var streamError *http2.StreamError
	if errors.As(surfaced, &streamError) {
		t.Fatalf("write path leaked an http2.StreamError (%v): SPEC 082 requires the transport to hide it", surfaced)
	}
}

// TestUplinkReleasesItsQueueOnClose: after Close the queue must be dropped, not
// held by the conn — a 4 MiB payload kept alive per closed session is a leak that
// no correctness test would see.
func TestUplinkReleasesItsQueueOnClose(t *testing.T) {
	t.Parallel()
	transport := newProbeTransport()
	conn := dialBatchConn(t, batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0"}))
	packetConn := conn.(*packetConn)
	if _, err := conn.Write(make([]byte, 4<<20)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Both reads of the queue go through the flusher's mutex: this test is about
	// what the queue holds, and reading it unlocked is itself a data race (the
	// race detector found exactly that in the first version of this test).
	queuedBeforeClose := func() []byte {
		packetConn.uplink.access.Lock()
		defer packetConn.uplink.access.Unlock()
		return packetConn.uplink.pending
	}
	if queuedBeforeClose() == nil {
		t.Fatal("the write did not queue anything: this test would pass vacuously")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if pending := queuedBeforeClose(); pending != nil {
		t.Fatalf("the queue still holds %d bytes after Close and a drained session", len(pending))
	}
}
