package trusttunnel

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// fakeSession is a sessionConn whose creation and stream answers the test
// decides, so the shared-offer and drain paths can be held open without a
// handshake.
type fakeSession struct {
	aliveOK    atomic.Bool
	closed     atomic.Bool
	roundTrips atomic.Int32
}

func newFakeSession() *fakeSession {
	session := &fakeSession{}
	session.aliveOK.Store(true)
	return session
}

func (s *fakeSession) roundTrip(ctx context.Context, authority string, header http.Header, withBody bool) (*rawStream, error) {
	s.roundTrips.Add(1)
	return &rawStream{
		response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}},
		reader:   strings.NewReader(""),
		writer:   io.Discard,
		closeFunc: func() {
		},
		wrapError: func(err error) error { return err },
	}, nil
}

func (s *fakeSession) alive() bool {
	return s.aliveOK.Load() && !s.closed.Load()
}

func (s *fakeSession) close() error {
	s.closed.Store(true)
	return nil
}

func newSharedOfferClient(t *testing.T, protocol string, dialSession func(context.Context) (sessionConn, error)) *Client {
	t.Helper()
	_, clientTLS := newTestTLS(t)
	client, err := NewClient(ClientOptions{
		Dialer:    &testDialer{},
		Server:    M.ParseSocksaddr("127.0.0.1:1"),
		TLSConfig: clientTLS,
		Protocol:  protocol,
		Username:  testUsername,
		Password:  testPassword,
		AppName:   "test",
	})
	require.NoError(t, err)
	if dialSession != nil {
		client.dialSession = dialSession
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestClientWaiterCancelKeepsTheSharedDial pins the waiter rule: a demand that
// joins a creation in flight can end its own wait, and doing so neither cancels
// the dial nor starts a second one.
func TestClientWaiterCancelKeepsTheSharedDial(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			var dials atomic.Int32
			client := newSharedOfferClient(t, protocol, func(ctx context.Context) (sessionConn, error) {
				dials.Add(1)
				started <- struct{}{}
				<-release
				return newFakeSession(), nil
			})
			ctx := testContext(t)
			first := make(chan net.Conn, 1)
			firstError := make(chan error, 1)
			go func() {
				conn, err := client.DialContext(ctx, holdDestination())
				if err != nil {
					firstError <- err
					return
				}
				first <- conn
			}()
			<-started

			shortCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			second := make(chan error, 1)
			go func() {
				_, err := client.DialContext(shortCtx, holdDestination())
				second <- err
			}()
			select {
			case err := <-second:
				require.ErrorIs(t, err, context.DeadlineExceeded)
			case <-time.After(3 * time.Second):
				t.Fatal("a waiter blocked on the shared dial ignored its own deadline")
			}
			cancel()
			require.EqualValues(t, 1, dials.Load(), "the cancelled waiter started a second dial")

			close(release)
			select {
			case err := <-firstError:
				t.Fatalf("the owner's dial failed: %v", err)
			case conn := <-first:
				require.NoError(t, conn.Close())
			case <-time.After(5 * time.Second):
				t.Fatal("the owner's dial did not finish")
			}
			require.EqualValues(t, 1, dials.Load())
		})
	}
}

// TestClientCloseDiscardsTheInFlightDial pins the owner rule: Close neither
// waits for the dial nor leaks its late success - the connection is closed
// exactly once and never published after the client ended.
func TestClientCloseDiscardsTheInFlightDial(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			late := newFakeSession()
			client := newSharedOfferClient(t, protocol, func(ctx context.Context) (sessionConn, error) {
				started <- struct{}{}
				<-release
				return late, nil
			})
			ctx := testContext(t)
			dialed := make(chan error, 1)
			go func() {
				_, err := client.DialContext(ctx, holdDestination())
				dialed <- err
			}()
			<-started

			closed := make(chan error, 1)
			go func() { closed <- client.Close() }()
			select {
			case err := <-closed:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("Close waited for the in-flight dial")
			}
			close(release)
			select {
			case err := <-dialed:
				require.Error(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("the dialer did not return after the client closed")
			}
			require.Eventually(t, func() bool { return late.closed.Load() }, 5*time.Second, 5*time.Millisecond,
				"the late session was published or leaked after Close")
			require.Zero(t, late.roundTrips.Load())
			client.access.Lock()
			require.Nil(t, client.session)
			client.access.Unlock()
		})
	}
}

// TestClientResetDiscardsTheInFlightDialAndStaysUsable pins the difference
// between Reset and Close: the in-flight creation is discarded, but the client
// keeps its lifetime and the next demand dials again.
func TestClientResetDiscardsTheInFlightDial(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			first := newFakeSession()
			second := newFakeSession()
			var dials atomic.Int32
			client := newSharedOfferClient(t, protocol, func(ctx context.Context) (sessionConn, error) {
				if dials.Add(1) == 1 {
					started <- struct{}{}
					<-release
					return first, nil
				}
				return second, nil
			})
			ctx := testContext(t)
			dialed := make(chan error, 1)
			go func() {
				_, err := client.DialContext(ctx, holdDestination())
				dialed <- err
			}()
			<-started
			client.Reset()
			close(release)
			select {
			case err := <-dialed:
				require.Error(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("the dialer did not return after Reset")
			}
			require.Eventually(t, func() bool { return first.closed.Load() }, 5*time.Second, 5*time.Millisecond)

			conn, err := client.DialContext(ctx, holdDestination())
			require.NoError(t, err)
			require.NoError(t, conn.Close())
			require.EqualValues(t, 2, dials.Load(), "Reset poisoned the client")
			require.False(t, second.closed.Load())
		})
	}
}

// TestClientDialFailureIsSharedByWaiters pins that one failed creation answers
// every waiter instead of each of them dialing again, and that a later demand
// is free to start a new one.
func TestClientDialFailureIsSharedByWaiters(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			release := make(chan struct{})
			var dials atomic.Int32
			client := newSharedOfferClient(t, protocol, func(ctx context.Context) (sessionConn, error) {
				if dials.Add(1) == 1 {
					<-release
				}
				return nil, errors.New("dial failed")
			})
			ctx := testContext(t)
			const callers = 4
			var waitGroup sync.WaitGroup
			waitGroup.Add(callers)
			errorsSeen := make(chan error, callers)
			for i := 0; i < callers; i++ {
				go func() {
					defer waitGroup.Done()
					_, err := client.DialContext(ctx, holdDestination())
					errorsSeen <- err
				}()
			}
			// Let the first demand start the creation and the rest join it.
			require.Eventually(t, func() bool { return dials.Load() == 1 }, 5*time.Second, time.Millisecond)
			time.Sleep(20 * time.Millisecond)
			close(release)
			waitGroup.Wait()
			close(errorsSeen)
			for err := range errorsSeen {
				require.Error(t, err)
			}
			require.EqualValues(t, 1, dials.Load(), "a waiter dialed its own session after the shared creation failed")

			_, err := client.DialContext(ctx, holdDestination())
			require.Error(t, err)
			require.EqualValues(t, 2, dials.Load(), "a demand after the failure must be able to dial again")
		})
	}
}

// TestClientRetireSuspectDrains pins the reuse boundary: the session held at the
// call takes no new stream, one with a stream open closes when that stream
// returns, and the next demand opens a fresh session.
func TestClientRetireSuspectDrains(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			var (
				access   sync.Mutex
				sessions []*fakeSession
			)
			client := newSharedOfferClient(t, protocol, func(ctx context.Context) (sessionConn, error) {
				session := newFakeSession()
				access.Lock()
				sessions = append(sessions, session)
				access.Unlock()
				return session, nil
			})
			ctx := testContext(t)
			held, err := client.DialContext(ctx, holdDestination())
			require.NoError(t, err)
			access.Lock()
			require.Len(t, sessions, 1)
			firstSession := sessions[0]
			access.Unlock()

			client.RetireSuspect()
			require.False(t, firstSession.closed.Load(), "a session with a stream was closed at the boundary")

			// The old session takes no new stream: this demand opens a second one.
			conn, err := client.DialContext(ctx, holdDestination())
			require.NoError(t, err)
			require.NoError(t, conn.Close())
			access.Lock()
			require.Len(t, sessions, 2)
			secondSession := sessions[1]
			access.Unlock()
			require.False(t, firstSession.closed.Load())

			// The last stream of the retired session returns: it closes there.
			require.NoError(t, held.Close())
			require.Eventually(t, func() bool { return firstSession.closed.Load() }, 5*time.Second, 5*time.Millisecond,
				"the retired session did not close after its last stream")
			require.False(t, secondSession.closed.Load())

			// A boundary with no stream closes at the call.
			client.RetireSuspect()
			require.True(t, secondSession.closed.Load(), "an idle session was not closed at the boundary")
		})
	}
}

// TestClientCloseIdleConnections pins the TRIM action: it closes an idle session
// and never touches one with a stream, and it never dials.
func TestClientCloseIdleConnections(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			var (
				access   sync.Mutex
				sessions []*fakeSession
			)
			client := newSharedOfferClient(t, protocol, func(ctx context.Context) (sessionConn, error) {
				session := newFakeSession()
				access.Lock()
				sessions = append(sessions, session)
				access.Unlock()
				return session, nil
			})
			ctx := testContext(t)
			held, err := client.DialContext(ctx, holdDestination())
			require.NoError(t, err)
			client.CloseIdleConnections()
			access.Lock()
			require.Len(t, sessions, 1)
			require.False(t, sessions[0].closed.Load(), "a session with a stream was trimmed")
			access.Unlock()

			require.NoError(t, held.Close())
			client.CloseIdleConnections()
			access.Lock()
			require.True(t, sessions[0].closed.Load(), "the idle session was not trimmed")
			access.Unlock()

			conn, err := client.DialContext(ctx, holdDestination())
			require.NoError(t, err)
			require.NoError(t, conn.Close())
			access.Lock()
			require.Len(t, sessions, 2, "CloseIdleConnections changed the next demand")
			access.Unlock()
		})
	}
}
