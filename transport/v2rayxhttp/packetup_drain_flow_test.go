package v2rayxhttp

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// failingBodyReader stands in for an upload response whose body dies after the headers.
//
// This is the half-dead peer SPEC 076's breaker exists for, and it is NOT the same event as
// a non-200: the server answered 200, so every status-based check is satisfied, and only the
// body read can tell "the exchange completed" apart from "the server went silent".
type failingBodyReader struct {
	remaining int
	err       error
}

func (r *failingBodyReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, r.err
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	r.remaining -= n
	return n, nil
}

func (r *failingBodyReader) Close() error { return nil }

// TestUplinkStalledResponseBodyIsNotGenuineDataFlow is the regression guard for a discarded
// error on the drain path.
//
// # The defect
//
// drainPacketUpResponse used to swallow the body read error:
//
//	_, _ = io.Copy(io.Discard, body)
//
// and sendPacket then called xmux.noteSuccess() unconditionally. A post whose 200 body stalled
// until the post's own deadline (packetUpPostTimeout) therefore RESET the pooled connection's
// failure streak and DISARMED the manager backoff, recording a wedged upload path as genuine
// data flow. SPEC 076's comment on that call names the field case it exists for: "200-raises
// whose bodies died seconds later".
//
// # Why the assertion is on the breaker and not on the returned error alone
//
// The error surfacing is only half the contract. The consequence that made this worth fixing is
// that the connection stopped being backed off, and a test that only checked "Write eventually
// failed" would pass against the defect as long as the post failure was reported some other way.
// So this asserts BOTH: the write path reports the drain failure, and the failure streak is NOT
// reset.
func TestUplinkStalledResponseBodyIsNotGenuineDataFlow(t *testing.T) {
	t.Parallel()

	drainErr := errors.New("connection reset while reading the upload response body")
	transport := newProbeTransport()
	transport.onPost = func(request *http.Request, body []byte) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       &failingBodyReader{remaining: 1, err: drainErr},
		}, nil
	}

	client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0", ScMaxEachPostBytes: "64-64"})
	conn := dialBatchConn(t, client)

	// Seed the breaker with a non-zero streak, so "the streak was reset" and "the streak was
	// never touched" are distinguishable. Without this the assertion below would pass against
	// the defect, because the defect RESETS to zero and a fresh client is already zero.
	packet, isPacket := conn.(*packetConn)
	if !isPacket {
		t.Fatalf("expected a *packetConn, got %T", conn)
	}
	packet.xmux.client.consecFails.Store(3)
	streakBefore := packet.xmux.client.consecFails.Load()

	if _, err := conn.Write([]byte("first")); err != nil {
		t.Fatalf("the first write only queues and must not fail: %v", err)
	}
	transport.waitForPosts(t, 1)

	// The post failed, so the next write must observe it rather than buffering into a queue
	// nothing will ever drain.
	var surfaced error
	deadline := time.Now().Add(writeFreeBudget)
	for surfaced == nil && time.Now().Before(deadline) {
		if _, err := conn.Write([]byte("second")); err != nil {
			surfaced = err
		}
		time.Sleep(5 * time.Millisecond)
	}
	if surfaced == nil {
		t.Fatal("the write path never reported the stalled response body")
	}
	if !strings.Contains(surfaced.Error(), "upload response body") {
		t.Fatalf("the write path reported %v, want the upload-response-body failure", surfaced)
	}

	if got := packet.xmux.client.consecFails.Load(); got != streakBefore {
		t.Fatalf("a post whose response body died was recorded as genuine data flow: the failure "+
			"streak moved from %d to %d, so the pooled connection stopped being backed off for a "+
			"wedged upload path (SPEC 076)", streakBefore, got)
	}

	// And the queue did not keep posting behind a failure: a hole in the sequence is exactly
	// what the one-post-in-flight argument forbids.
	_, paths, posts, _ := transport.snapshot()
	if posts != 1 {
		t.Fatalf("saw %d posts for one stalled post: nothing may follow a post that failed", posts)
	}
	if seqOf(t, paths[0]) != 0 {
		t.Fatalf("the only post should be seq 0, got %s", paths[0])
	}
}

// TestUplinkCompletedResponseBodyIsGenuineDataFlow is the positive control.
//
// Without it the test above could be satisfied by a change that simply never called noteSuccess,
// which would leave a healthy pooled connection permanently backed off, trading one silent
// failure for another. The two tests differ only in whether the body completes.
func TestUplinkCompletedResponseBodyIsGenuineDataFlow(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	transport.onPost = func(request *http.Request, body []byte) (*http.Response, error) {
		// A body that is read to EOF: the ordinary answer from an XHTTP upload endpoint.
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}

	client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0", ScMaxEachPostBytes: "64-64"})
	conn := dialBatchConn(t, client)

	packet, isPacket := conn.(*packetConn)
	if !isPacket {
		t.Fatalf("expected a *packetConn, got %T", conn)
	}
	packet.xmux.client.consecFails.Store(3)

	if _, err := conn.Write([]byte("first")); err != nil {
		t.Fatalf("the first write only queues and must not fail: %v", err)
	}
	transport.waitForPosts(t, 1)

	deadline := time.Now().Add(writeFreeBudget)
	for packet.xmux.client.consecFails.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := packet.xmux.client.consecFails.Load(); got != 0 {
		t.Fatalf("a completed 200 upload POST must reset the failure streak and disarm the backoff "+
			"(SPEC 076); the streak is still %d", got)
	}
}

// Keep the net import meaningful: the conn under test is asserted to be a net.Conn, which is the
// shape the production dialer hands to the router.
var _ net.Conn = (*packetConn)(nil)
