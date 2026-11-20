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

	"github.com/stretchr/testify/require"
)

// The borrow window attack (audit P03).
//
// getSession used to hand a session to its caller and only then, inside roundTrip, count the CONNECT
// as a stream. Between those two points the session looked idle to everything that decides "is this
// carrier unused?": CloseIdleConnections, RetireSuspect's drainIfUnused and the idle watcher. A trim
// that won that race closed the carrier the caller was already holding, so the CONNECT that followed
// could not be sent. On a reused session the demand paid a second session creation; on a session the
// same call had just created, the demand failed outright, because a fresh session's CONNECT is not
// retried on another carrier.
//
// These two attacks pin the rule at both ends: the slot must be reserved before the lock is dropped
// in getSession, and the reservation must be given back on every path that does not end in a stream.

// trimWindowSession behaves like a real carrier in the one respect these tests need: a CONNECT sent
// after close cannot go anywhere. It counts the CONNECTs it carried and, separately, the ones that
// arrived after a trim had already closed it - the damage a trim promises never to do.
type trimWindowSession struct {
	connects           atomic.Int32
	connectsAfterClose atomic.Int32
	closed             atomic.Bool
}

func newTrimWindowSession() *trimWindowSession {
	return &trimWindowSession{}
}

func (s *trimWindowSession) roundTrip(ctx context.Context, authority string, header http.Header, withBody bool) (*rawStream, error) {
	if s.closed.Load() {
		s.connectsAfterClose.Add(1)
		return nil, net.ErrClosed
	}
	s.connects.Add(1)
	return &rawStream{
		response:  &http.Response{StatusCode: http.StatusOK, Header: http.Header{}},
		reader:    strings.NewReader(""),
		writer:    io.Discard,
		closeFunc: func() {},
		wrapError: func(err error) error { return err },
	}, nil
}

func (s *trimWindowSession) alive() bool { return !s.closed.Load() }

func (s *trimWindowSession) close() error {
	s.closed.Store(true)
	return nil
}

// trimWindowDialer records every session the client creates, so a test can tell "reused the carrier
// it was handed" from "rebuilt it after a trim closed it".
type trimWindowDialer struct {
	mu       sync.Mutex
	sessions []*trimWindowSession
}

func (d *trimWindowDialer) dial(context.Context) (sessionConn, error) {
	session := newTrimWindowSession()
	d.mu.Lock()
	d.sessions = append(d.sessions, session)
	d.mu.Unlock()
	return session, nil
}

func (d *trimWindowDialer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sessions)
}

func (d *trimWindowDialer) at(index int) *trimWindowSession {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions[index]
}

// armTrimWindowHook runs action at the exact point where getSession has handed the session to the
// caller but the caller has not sent its CONNECT yet.
func armTrimWindowHook(t *testing.T, action func()) {
	t.Helper()
	var once sync.Once
	testHookSessionHandedOff = func(*clientSession) {
		once.Do(action)
	}
	t.Cleanup(func() { testHookSessionHandedOff = nil })
}

// TestFreshSessionHandedToACallerSurvivesATrim is the sharper half: the session was created by this
// very demand, so a trim in the borrow window used to fail the request instead of costing a retry.
func TestFreshSessionHandedToACallerSurvivesATrim(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			dialer := &trimWindowDialer{}
			client := newSharedOfferClient(t, protocol, dialer.dial)
			trimmed := false
			armTrimWindowHook(t, func() {
				trimmed = true
				client.CloseIdleConnections()
			})

			conn, err := client.DialContext(testContext(t), holdDestination())
			require.True(t, trimmed, "the attack did not reach the borrow window")
			require.NoError(t, err, "a trim closed the carrier the demand had just been handed")
			require.NotNil(t, conn)
			require.Equal(t, 1, dialer.count(), "the demand rebuilt the session it had just been handed")

			session := dialer.at(0)
			require.Equal(t, int32(0), session.connectsAfterClose.Load(),
				"a CONNECT was sent on a carrier a trim had closed")
			require.Equal(t, int32(1), session.connects.Load())
			require.NoError(t, conn.Close())
		})
	}
}

// TestReusedSessionHandedToACallerSurvivesATrim covers the other half: the slot a second demand takes
// on an already-built session must be visible to the trim as well, or every request that races a
// reclamation pays a fresh connection.
func TestReusedSessionHandedToACallerSurvivesATrim(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			dialer := &trimWindowDialer{}
			client := newSharedOfferClient(t, protocol, dialer.dial)

			first, err := client.DialContext(testContext(t), holdDestination())
			require.NoError(t, err)
			require.NoError(t, first.Close())
			require.Equal(t, 1, dialer.count())

			trimmed := false
			armTrimWindowHook(t, func() {
				trimmed = true
				client.CloseIdleConnections()
			})
			second, err := client.DialContext(testContext(t), holdDestination())
			require.True(t, trimmed, "the attack did not reach the borrow window")
			require.NoError(t, err, "the second demand lost the session it was handed to a trim")
			require.Equal(t, 1, dialer.count(), "the demand rebuilt a session a trim should not have closed")

			session := dialer.at(0)
			require.Equal(t, int32(0), session.connectsAfterClose.Load(),
				"a CONNECT was sent on a carrier a trim had closed")
			require.Equal(t, int32(2), session.connects.Load())
			require.NoError(t, second.Close())
		})
	}
}

// TestHealthProbeHandedASessionSurvivesATrim is the third entry into the same window: Check takes a
// session from getSession and sends CONNECT _check without ever creating a stream, so it depended on
// the same missing reservation. A trim landing there used to fail the probe and tear the carrier down.
func TestHealthProbeHandedASessionSurvivesATrim(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			dialer := &trimWindowDialer{}
			client := newSharedOfferClient(t, protocol, dialer.dial)

			first, err := client.DialContext(testContext(t), holdDestination())
			require.NoError(t, err)
			require.NoError(t, first.Close())

			trimmed := false
			armTrimWindowHook(t, func() {
				trimmed = true
				client.CloseIdleConnections()
			})
			require.NoError(t, client.Check(testContext(t)),
				"a trim closed the carrier under the health probe it had just been handed")
			require.True(t, trimmed, "the attack did not reach the borrow window")
			require.Equal(t, 1, dialer.count(), "the probe rebuilt a session a trim should not have closed")

			session := dialer.at(0)
			require.Equal(t, int32(0), session.connectsAfterClose.Load(),
				"the probe was sent on a carrier a trim had closed")
			require.False(t, session.closed.Load(), "the probe's failed retirement closed the carrier")
		})
	}
}

// failingSession refuses every CONNECT. It is the leak control for the reservation protocol: a failed
// attempt must give its slot back, or the session stays "active" forever, no trim can ever reclaim it,
// and the carrier leaks for the life of the client.
type failingSession struct {
	closed atomic.Bool
}

func (s *failingSession) roundTrip(context.Context, string, http.Header, bool) (*rawStream, error) {
	return nil, io.ErrUnexpectedEOF
}

func (s *failingSession) alive() bool { return !s.closed.Load() }

func (s *failingSession) close() error {
	s.closed.Store(true)
	return nil
}

func TestFailedConnectReturnsItsReservedSlot(t *testing.T) {
	session := &failingSession{}
	var dials atomic.Int32
	client := newSharedOfferClient(t, ProtocolHTTP2, func(context.Context) (sessionConn, error) {
		dials.Add(1)
		return session, nil
	})

	conn, err := client.DialContext(testContext(t), holdDestination())
	require.Error(t, err)
	require.Nil(t, conn)
	require.Equal(t, int32(1), dials.Load(), "a failed CONNECT on a fresh session must not be retried")
	require.True(t, session.closed.Load(),
		"the failed attempt leaked its reserved slot, so the session was never reclaimed")
}
