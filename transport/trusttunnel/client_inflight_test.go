package trusttunnel

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// inflightSession blocks one CONNECT until the test releases it, which is the window in which a
// trim or RetireSuspect used to see streams==0 and close the carrier under the request.
type inflightSession struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	closed      atomic.Bool
}

func newInflightSession() *inflightSession {
	return &inflightSession{started: make(chan struct{}), release: make(chan struct{})}
}

func (s *inflightSession) roundTrip(ctx context.Context, authority string, header http.Header, withBody bool) (*rawStream, error) {
	s.startedOnce.Do(func() { close(s.started) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &rawStream{
		response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}},
		reader:   strings.NewReader(""),
		writer:   io.Discard,
		closeFunc: func() {
		},
		wrapError: func(err error) error { return err },
	}, nil
}

func (s *inflightSession) alive() bool { return !s.closed.Load() }

func (s *inflightSession) close() error {
	s.closed.Store(true)
	return nil
}

// TestInFlightConnectIsCountedAsAStream is the regression for the retire/trim window: both actions
// must observe the CONNECT that is already on the wire and drain the session instead of closing it.
func TestInFlightConnectIsCountedAsAStream(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			session := newInflightSession()
			client := newSharedOfferClient(t, protocol, func(context.Context) (sessionConn, error) {
				return session, nil
			})

			type dialResult struct {
				conn net.Conn
				err  error
			}
			result := make(chan dialResult, 1)
			go func() {
				conn, err := client.DialContext(testContext(t), holdDestination())
				result <- dialResult{conn: conn, err: err}
			}()
			select {
			case <-session.started:
			case <-time.After(5 * time.Second):
				t.Fatal("the CONNECT never reached the fake session")
			}

			client.CloseIdleConnections()
			require.False(t, session.closed.Load(), "a trim closed a session with an in-flight CONNECT")
			client.RetireSuspect()
			require.False(t, session.closed.Load(), "the reuse boundary closed a session with an in-flight CONNECT")

			close(session.release)
			var dialed dialResult
			select {
			case dialed = <-result:
			case <-time.After(5 * time.Second):
				t.Fatal("the in-flight CONNECT never returned")
			}
			require.NoError(t, dialed.err)
			require.NotNil(t, dialed.conn)
			require.False(t, session.closed.Load(), "the retired session closed while its accepted stream was still open")

			require.NoError(t, dialed.conn.Close())
			require.Eventually(t, func() bool { return session.closed.Load() }, 5*time.Second, time.Millisecond,
				"the drained session did not close after its last stream returned")
		})
	}
}
