package v2rayxhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
)

// roundTripperFunc adapts a function to http.RoundTripper for these cases.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// contextBody blocks until the request context ends, which is what a pending
// XHTTP download body looks like to a reader.
type contextBody struct {
	ctx context.Context
}

func (b *contextBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *contextBody) Close() error { return nil }

// blockingReadBody blocks forever if Read is called. The refusal path must
// close it without draining it, so a drain shows up as a parked dial instead
// of a test that passes because the body happened to return EOF.
type blockingReadBody struct {
	closed atomic.Bool
}

func (b *blockingReadBody) Read([]byte) (int, error) { select {} }

func (b *blockingReadBody) Close() error {
	b.closed.Store(true)
	return nil
}

// TestStreamUpDialFailsOnUploadBadStatus is the stream-up refusal case:
// the paired download GET is still pending when the upload POST comes back
// non-200. The server has refused the stream, so the dial must end with that
// status instead of parking until the caller's context and leaving the
// download RoundTrip alive. The body blocks on Read, so a drain would hang.
func TestStreamUpDialFailsOnUploadBadStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusNotFound,
		http.StatusRequestEntityTooLarge,
		http.StatusInternalServerError,
	} {
		status := status
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			hang := &hangRoundTripper{}
			refusalBody := &blockingReadBody{}
			client := stubClient(t, modeStreamUp, methodRoundTripper{
				get: hang,
				post: roundTripperFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: status,
						Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
						Body:       refusalBody,
						Header:     make(http.Header),
					}, nil
				}),
			})

			err := expectDialFailure(t, dialUnderTest(context.Background(), client), http.StatusText(status))
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				t.Fatalf("DialContext error %q is a context error, want the upload status", err)
			}
			if !refusalBody.closed.Load() {
				t.Fatal("the refused upload response body was not closed")
			}
			waitObserved(t, hang, 1, "pending download RoundTrip not aborted by the failed dial")
			if got := openUsageOf(client); got != 0 {
				t.Fatalf("openUsage = %d after a failed dial, want 0", got)
			}
		})
	}
}

// TestSplitConnWriteCarriesTheUploadRefusal covers the post-raise half: when
// the dial already handed the conn up, a refused upload must break the upload
// pipe with the status error, not surface as a bare ErrClosedPipe or a hang.
func TestSplitConnWriteCarriesTheUploadRefusal(t *testing.T) {
	reader, writer := io.Pipe()
	conn := newSplitConn(reader, writer, M.ParseSocksaddr("127.0.0.1:443"), nil)
	refusal := errors.New("upload refused with 400")
	conn.uploadFailed(refusal)
	if _, err := conn.Write([]byte("x")); !errors.Is(err, refusal) {
		t.Fatalf("Write error = %v, want %v", err, refusal)
	}
}

// TestStreamUpRaisedConnFailsOnLateUploadRefusal covers the half that arrives
// after the conn was handed up: the download already answered 200 and a reader
// is waiting on it when the upload POST is refused. Both directions must end
// with the refusal instead of the reader waiting for its deadline.
func TestStreamUpRaisedConnFailsOnLateUploadRefusal(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	client := stubClient(t, modeStreamUp, methodRoundTripper{
		get: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       &contextBody{ctx: request.Context()},
				Header:     make(http.Header),
			}, nil
		}),
		post: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			<-gate
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Status:     "400 Bad Request",
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}),
	})

	result := dialUnderTest(context.Background(), client)
	var conn net.Conn
	select {
	case raised := <-result:
		if raised.err != nil {
			t.Fatalf("dial failed on a live download: %v", raised.err)
		}
		typed, ok := raised.conn.(net.Conn)
		if !ok {
			t.Fatalf("dial returned %T, want net.Conn", raised.conn)
		}
		conn = typed
	case <-time.After(writeFreeBudget):
		t.Fatal("dial did not raise on the download 200")
	}
	defer conn.Close()

	close(gate)
	writeErr := make(chan error, 1)
	go func() {
		_, err := conn.Write([]byte("x"))
		writeErr <- err
	}()
	select {
	case err := <-writeErr:
		if err == nil || !strings.Contains(err.Error(), http.StatusText(http.StatusBadRequest)) {
			t.Fatalf("Write error = %v, want the upload refusal", err)
		}
	case <-time.After(writeFreeBudget):
		t.Fatal("Write did not surface the upload refusal")
	}

	_ = conn.SetReadDeadline(time.Now().Add(writeFreeBudget))
	_, err := conn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("Read succeeded after the upload was refused")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("Read only saw its own deadline: %v", err)
	}
}
