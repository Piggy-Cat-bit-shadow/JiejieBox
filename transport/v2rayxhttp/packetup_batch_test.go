package v2rayxhttp

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// lx: XHTTP_OUTPERFORM — the packet-up uplink queue.
//
// These tests pin the contract that replaced "one HTTP POST per Write":
//
//  1. a write is queued and Write returns once it is buffered, not once a post
//     has been answered;
//  2. writes that queue up behind an in-flight post leave as ONE post, up to
//     sc_max_each_post_bytes;
//  3. order and seq are preserved across posts — the server's reorder buffer is
//     what reassembles them, so a batching bug is a data-corruption bug;
//  4. Close delivers the buffered tail instead of dropping it, which is what a
//     relay does with the last bytes of a request;
//  5. the queue is bounded, so a stalled path becomes backpressure rather than
//     unbounded memory;
//  6. a post failure is bounded and is reported to the write path.
//
// The measured reason all of this exists is in packetUplink's doc comment: at
// one post per write, sc_min_posts_interval_ms caps a stream at (bytes per
// write)/interval, and every write also pays its own round trip.

// recordingTransport answers 200, records every upload POST body in arrival
// order, and can hold the FIRST post until released, so a test controls exactly
// what queues up behind an in-flight post.
type recordingTransport struct {
	access  sync.Mutex
	bodies  [][]byte
	paths   []string
	times   []time.Time
	entered chan struct{}
	release chan struct{}
	gate    bool
	once    sync.Once
}

func newRecordingTransport(gateFirst bool) *recordingTransport {
	return &recordingTransport{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		gate:    gateFirst,
	}
}

func (rt *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodGet {
		// The download stream stays open, like a server that withholds downlink
		// until it has something to send.
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(newBlockingReader()),
		}, nil
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	request.Body.Close()
	rt.access.Lock()
	first := len(rt.bodies) == 0
	rt.bodies = append(rt.bodies, body)
	rt.paths = append(rt.paths, request.URL.Path)
	rt.times = append(rt.times, time.Now())
	rt.access.Unlock()
	if first {
		rt.once.Do(func() { close(rt.entered) })
		if rt.gate {
			<-rt.release
		}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

func (rt *recordingTransport) recorded() [][]byte {
	rt.access.Lock()
	defer rt.access.Unlock()
	out := make([][]byte, len(rt.bodies))
	copy(out, rt.bodies)
	return out
}

func (rt *recordingTransport) recordedPaths() []string {
	rt.access.Lock()
	defer rt.access.Unlock()
	out := make([]string, len(rt.paths))
	copy(out, rt.paths)
	return out
}

// delivered reports whether any recorded post carried the payload, and when the
// first such post arrived.
func (rt *recordingTransport) delivered(payload string) (time.Time, bool) {
	rt.access.Lock()
	defer rt.access.Unlock()
	for index, body := range rt.bodies {
		if strings.Contains(string(body), payload) {
			return rt.times[index], true
		}
	}
	return time.Time{}, false
}

func (rt *recordingTransport) waitForPosts(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(writeFreeBudget)
	for {
		rt.access.Lock()
		count := len(rt.bodies)
		rt.access.Unlock()
		if count >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("saw %d upload posts, want %d", count, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// batchClient is a packet-up client with the post interval disabled, so a test
// measures batching rather than the anti-burst throttle.
func batchClient(t *testing.T, transport http.RoundTripper, options metaOptions) *Client {
	t.Helper()
	if options.ScMinPostsIntervalMs == "" {
		options.ScMinPostsIntervalMs = "0"
	}
	meta, err := normalizeMeta(options, modePacketUp)
	if err != nil {
		t.Fatalf("normalizeMeta: %v", err)
	}
	client := stubClient(t, modePacketUp, transport)
	client.meta = meta
	return client
}

func dialBatchConn(t *testing.T, client *Client) net.Conn {
	t.Helper()
	conn, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// TestPacketUpWriteBuffersInsteadOfWaitingForThePost is the contract the whole
// change rests on: Write accepts bytes into the queue and returns, and the post
// happens behind it. Before this, Write returned only after its own POST had
// been answered, which is what made a handshake cost two posts and two round
// trips.
func TestPacketUpWriteBuffersInsteadOfWaitingForThePost(t *testing.T) {
	t.Parallel()
	transport := newRecordingTransport(true)
	conn := dialBatchConn(t, batchClient(t, transport, metaOptions{}))

	started := time.Now()
	if _, err := conn.Write([]byte("first")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if elapsed := time.Since(started); elapsed > writeFreeBudget/10 {
		t.Fatalf("Write blocked for %s on a post it should only have queued", elapsed)
	}
	// The post is now in flight and gated; the write path must not be waiting on
	// it. Release the gate so the cleanup path can drain.
	select {
	case <-transport.entered:
	case <-time.After(writeFreeBudget):
		t.Fatal("the queued write never reached a post")
	}
	close(transport.release)
}

// TestPacketUpCoalescesWritesQueuedBehindAPost is the throughput fix in its
// smallest form: bytes that queue while a post is in flight leave as ONE post,
// in order.
func TestPacketUpCoalescesWritesQueuedBehindAPost(t *testing.T) {
	t.Parallel()
	transport := newRecordingTransport(true)
	conn := dialBatchConn(t, batchClient(t, transport, metaOptions{}))

	if _, err := conn.Write([]byte("aaa")); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	// Wait until the first post owns the wire: what follows then provably queued
	// behind it rather than racing it.
	select {
	case <-transport.entered:
	case <-time.After(writeFreeBudget):
		t.Fatal("the first write never reached a post")
	}
	for _, payload := range []string{"bbb", "ccc", "ddd"} {
		if _, err := conn.Write([]byte(payload)); err != nil {
			t.Fatalf("Write(%q): %v", payload, err)
		}
	}
	close(transport.release)
	transport.waitForPosts(t, 2)

	bodies := transport.recorded()
	if len(bodies) != 2 {
		t.Fatalf("saw %d posts, want 2 (the first, then one carrying the queued three)", len(bodies))
	}
	if got := string(bodies[0]); got != "aaa" {
		t.Fatalf("first post body = %q, want %q", got, "aaa")
	}
	if got := string(bodies[1]); got != "bbbcccddd" {
		t.Fatalf("second post body = %q, want %q — queued writes were not coalesced", got, "bbbcccddd")
	}
}

// TestPacketUpBatchRespectsMaxEachPostBytes: sc_max_each_post_bytes caps a post,
// and the excess becomes further posts — in order, without loss.
func TestPacketUpBatchRespectsMaxEachPostBytes(t *testing.T) {
	t.Parallel()
	transport := newRecordingTransport(false)
	conn := dialBatchConn(t, batchClient(t, transport, metaOptions{ScMaxEachPostBytes: "100-100"}))

	payload := make([]byte, 350)
	for index := range payload {
		payload[index] = byte('a' + index%26)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	bodies := transport.recorded()
	if len(bodies) != 4 {
		t.Fatalf("saw %d posts for 350 bytes capped at 100, want 4", len(bodies))
	}
	var reassembled []byte
	for index, body := range bodies {
		if len(body) > 100 {
			t.Fatalf("post %d carried %d bytes, over the 100-byte cap", index, len(body))
		}
		if index < len(bodies)-1 && len(body) != 100 {
			t.Fatalf("post %d carried %d bytes, want a full 100 before the tail", index, len(body))
		}
		reassembled = append(reassembled, body...)
	}
	if string(reassembled) != string(payload) {
		t.Fatalf("reassembled %d bytes, want the %d written — a capped batch lost or reordered data",
			len(reassembled), len(payload))
	}
	// The server reassembles by seq, so the seq carried in the path must be
	// strictly increasing across posts.
	paths := transport.recordedPaths()
	for index := 1; index < len(paths); index++ {
		if paths[index] == paths[index-1] {
			t.Fatalf("posts %d and %d share the request path %q: seq did not advance", index-1, index, paths[index])
		}
	}
}

// TestPacketUpHandshakeIsNotThrottled pins the exemption in nextPostDelay: the
// post that carries the first payload leaves promptly even when it is a separate
// post from the VLESS header, while the interval still applies to the posts after
// it.
//
// The gap between the two writes stands in for the inbound's own round trip
// (answer the caller, read from it, relay) — local work in the hundreds of
// microseconds to a few milliseconds, and enough to miss any sub-millisecond
// timing window. The interval is raised to 300 ms so the two cases are separated
// by two orders of magnitude rather than by a budget that a race-instrumented run
// can eat: this test must fail on the mechanism, not on a slow machine.
func TestPacketUpHandshakeIsNotThrottled(t *testing.T) {
	t.Parallel()
	const interval = 300 * time.Millisecond
	transport := newRecordingTransport(false)
	client := stubClient(t, modePacketUp, transport)
	meta, err := normalizeMeta(metaOptions{ScMinPostsIntervalMs: "300-300"}, modePacketUp)
	if err != nil {
		t.Fatalf("normalizeMeta: %v", err)
	}
	client.meta = meta
	conn := dialBatchConn(t, client)

	waitForPayload := func(payload string) time.Duration {
		t.Helper()
		started := time.Now()
		deadline := started.Add(writeFreeBudget)
		for {
			if _, delivered := transport.delivered(payload); delivered {
				return time.Since(started)
			}
			if time.Now().After(deadline) {
				t.Fatalf("%q never reached a post", payload)
			}
			time.Sleep(time.Millisecond)
		}
	}

	if _, err = conn.Write([]byte("header")); err != nil {
		t.Fatalf("header write: %v", err)
	}
	waitForPayload("header")
	time.Sleep(20 * time.Millisecond)

	if _, err = conn.Write([]byte("payload")); err != nil {
		t.Fatalf("payload write: %v", err)
	}
	handshake := waitForPayload("payload")
	if handshake > interval/3 {
		t.Fatalf("the handshake payload took %s to reach a post with a %s interval: the opening exchange paid the anti-burst throttle", handshake, interval)
	}

	// The post after the opening pair is NOT exempt: it must wait out the
	// interval, which is what keeps this an exemption rather than a removal.
	if _, err = conn.Write([]byte("third")); err != nil {
		t.Fatalf("third write: %v", err)
	}
	throttled := waitForPayload("third")
	if throttled < interval*2/3 {
		t.Fatalf("the third post left after %s with a %s interval: the anti-burst throttle no longer applies", throttled, interval)
	}
}

// TestPacketUpCloseDeliversTheBufferedTail: the relay writes the last bytes of a
// request and closes immediately. Buffering must not turn that into data loss.
func TestPacketUpCloseDeliversTheBufferedTail(t *testing.T) {
	t.Parallel()
	transport := newRecordingTransport(false)
	conn := dialBatchConn(t, batchClient(t, transport, metaOptions{}))

	if _, err := conn.Write([]byte("the tail")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	bodies := transport.recorded()
	if len(bodies) == 0 {
		t.Fatal("Close dropped the buffered tail: no upload post was ever sent")
	}
	var reassembled []byte
	for _, body := range bodies {
		reassembled = append(reassembled, body...)
	}
	if string(reassembled) != "the tail" {
		t.Fatalf("posts carried %q, want %q", reassembled, "the tail")
	}
}

// TestPacketUpQueueAppliesBackpressure: the queue is bounded, so a stalled path
// blocks the writer instead of buffering without limit. The bound is derived
// from sc_max_each_post_bytes (with a floor), which is what this test configures
// down to keep the data small.
func TestPacketUpQueueAppliesBackpressure(t *testing.T) {
	t.Parallel()
	transport := newRecordingTransport(true)
	client := batchClient(t, transport, metaOptions{ScMaxEachPostBytes: "30000-30000"})
	conn := dialBatchConn(t, client)
	bound := conn.(*packetConn).uplink.maxPending
	if bound > packetUpPendingFloor {
		t.Fatalf("test expects the queue bound to sit at its floor, got %d", bound)
	}

	// One write starts a post that is gated; the rest fill the queue and then
	// have to wait.
	if _, err := conn.Write([]byte("start")); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	select {
	case <-transport.entered:
	case <-time.After(writeFreeBudget):
		t.Fatal("the first write never reached a post")
	}

	blocked := make(chan struct{})
	go func() {
		defer close(blocked)
		chunk := make([]byte, 8*1024)
		for written := 0; written < bound*2; written += len(chunk) {
			if _, err := conn.Write(chunk); err != nil {
				return
			}
		}
	}()
	select {
	case <-blocked:
		t.Fatal("writes past the queue bound completed while the path was stalled: the queue is unbounded")
	case <-time.After(200 * time.Millisecond):
	}

	// Releasing the post drains the queue and the writer finishes.
	close(transport.release)
	select {
	case <-blocked:
	case <-time.After(writeFreeBudget):
		t.Fatal("writer still blocked after the stalled post was released")
	}
}

// TestPacketUpPostFailureIsBoundedAndSurfaces lived here while the batching
// change was being made; its invariant belongs to the raise-failure contract and
// is pinned by TestPacketUpPostBounded in raise_failure_test.go.
