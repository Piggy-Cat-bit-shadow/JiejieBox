package trusttunnel

// The runtime failure of an ALREADY ESTABLISHED HTTP/3 session.
//
// The establishment-phase fallback (client_fallback_test.go) covers "H3 could not be opened". This
// file covers the failure the incident actually produced: the session opened, carried traffic, and
// then stopped answering new CONNECTs while quic-go still reported the connection alive. The
// contract is:
//
//   - the wait for an answer is bounded even when the session's silence is short (P1-D);
//   - a timeout corroborated by "nothing received" retires the session and, on HTTP/3 with the
//     fallback configured, arms the HTTP/2 verdict (P1-A/B);
//   - the retry goes to the HTTP/2 carrier, so the caller that hit the dead carrier recovers
//     without a restart;
//   - one slow target while the carrier is demonstrably receiving must NOT retire it, cordon H3 or
//     pay the establishment budget twice.

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// stalledSession accepts a CONNECT and never answers it while its context is alive. This is the
// shape of an HTTP/3 carrier whose UDP path stopped delivering: the QUIC connection itself is not
// closed, so alive() keeps returning true.
type stalledSession struct {
	entered atomic.Int32
	closed  atomic.Bool
}

func (s *stalledSession) roundTrip(ctx context.Context, authority string, header http.Header, withBody bool) (*rawStream, error) {
	s.entered.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *stalledSession) alive() bool { return !s.closed.Load() }

func (s *stalledSession) close() error {
	s.closed.Store(true)
	return nil
}

// selectiveSession answers the fast authority immediately and stalls the slow one. The test keeps
// the client session's receive clock moving while the slow CONNECT is in flight, which is exactly
// the situation of a video stream that keeps working while one target's connect is slow.
type selectiveSession struct {
	fast      atomic.Int32
	slow      atomic.Int32
	closed    atomic.Bool
	closeFunc func()
}

func (s *selectiveSession) roundTrip(ctx context.Context, authority string, header http.Header, withBody bool) (*rawStream, error) {
	if authority == "slow.test:443" {
		s.slow.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.fast.Add(1)
	return &rawStream{
		response:  &http.Response{StatusCode: http.StatusOK, Header: http.Header{}},
		reader:    nil,
		writer:    nil,
		closeFunc: func() {},
		wrapError: func(err error) error { return err },
	}, nil
}

func (s *selectiveSession) alive() bool { return !s.closed.Load() }

func (s *selectiveSession) close() error {
	s.closed.Store(true)
	if s.closeFunc != nil {
		s.closeFunc()
	}
	return nil
}

func TestRuntimeH3SilenceRetiresAndFallsBack(t *testing.T) {
	h3 := &stalledSession{}
	h2 := newFakeSession()
	var h3Dials, h2Dials atomic.Int32
	client, _ := newFallbackTestClient(t,
		func(context.Context) (sessionConn, error) { h3Dials.Add(1); return h3, nil },
		func(context.Context) (sessionConn, error) { h2Dials.Add(1); return h2, nil },
	)
	client.connectBudget = 40 * time.Millisecond
	client.creationBudget = 2 * time.Second
	client.h3Cooldown = time.Minute

	ctx := testContext(t)
	started := time.Now()
	conn, err := client.DialContext(ctx, fallbackDestination)
	require.NoError(t, err, "a request that met the dead H3 carrier must recover over HTTP/2")
	require.Less(t, time.Since(started), 5*time.Second)
	require.NoError(t, conn.Close())

	require.EqualValues(t, 1, h3Dials.Load())
	require.EqualValues(t, 1, h2Dials.Load(), "the recovery session was not the HTTP/2 leg")
	require.EqualValues(t, 1, h2.roundTrips.Load(), "the retried CONNECT did not run on HTTP/2")
	require.True(t, h3.closed.Load(), "the silent H3 session was not released")
	require.True(t, client.h3Cordoned(), "the runtime failure did not arm the HTTP/2 verdict")

	// The recovered session is the published one and carries the next demand without a new dial.
	next, err := client.DialContext(ctx, fallbackDestination)
	require.NoError(t, err)
	require.NoError(t, next.Close())
	require.EqualValues(t, 1, h3Dials.Load())
	require.EqualValues(t, 1, h2Dials.Load())
}

// TestCallerDeadlineIsNotCarrierSilence pins the classification boundary between the caller's own
// deadline and the client's establishment budget. When the caller's deadline is the tighter bound
// the parent context is used unchanged, so the error stays context.DeadlineExceeded and the carrier
// is not retired or cordoned: an impatient caller is not evidence about the node.
// TestFailedHealthCheckRetiresAndCordons pins the optional _check path: when health_check is
// enabled and the probe fails, the session stops being reused and HTTP/3 gets the same provisional
// verdict a stalled CONNECT gets. The option stays off by default; this only proves that enabling it
// has the intended effect instead of being a field that changes nothing.
func TestFailedHealthCheckRetiresAndCordons(t *testing.T) {
	probe := &failingProbeSession{}
	client := newSharedOfferClient(t, ProtocolHTTP3, func(context.Context) (sessionConn, error) {
		return probe, nil
	})
	require.True(t, quicIncluded)
	client.quicFallback = true
	client.h3Cooldown = time.Minute
	client.healthCheck = true

	session := newClientSession(context.Background(), probe)
	session.id = 1
	session.carrier = ProtocolHTTP3
	client.access.Lock()
	client.session = session
	client.access.Unlock()

	err := client.check(context.Background(), nil, session)
	require.Error(t, err)
	client.access.Lock()
	published := client.session
	client.access.Unlock()
	require.Nil(t, published, "a session that failed its health check was kept for reuse")
	require.True(t, client.h3Cordoned(), "a failed HTTP/3 health check did not arm the HTTP/2 verdict")
}

type failingProbeSession struct {
	closed atomic.Bool
}

func (s *failingProbeSession) roundTrip(context.Context, string, http.Header, bool) (*rawStream, error) {
	return nil, errors.New("probe failed")
}

func (s *failingProbeSession) alive() bool { return !s.closed.Load() }

func (s *failingProbeSession) close() error {
	s.closed.Store(true)
	return nil
}

func TestCallerDeadlineIsNotCarrierSilence(t *testing.T) {
	h3 := &stalledSession{}
	client := newSharedOfferClient(t, ProtocolHTTP3, func(context.Context) (sessionConn, error) {
		return h3, nil
	})
	require.True(t, quicIncluded)

	ctx, cancel := context.WithTimeout(testContext(t), 60*time.Millisecond)
	defer cancel()
	_, err := client.DialContext(ctx, fallbackDestination)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotErrorIs(t, err, errConnectEstablishTimeout,
		"the caller's deadline was reported as the client's establishment budget")
	client.access.Lock()
	session := client.session
	client.access.Unlock()
	require.NotNil(t, session, "the carrier was retired because the caller's deadline expired")
	require.False(t, session.retiring.Load())
	require.False(t, h3.closed.Load())
}

func TestOneSlowTargetDoesNotRetireAReceivingCarrier(t *testing.T) {
	slow := &selectiveSession{}
	client := newSharedOfferClient(t, ProtocolHTTP3, func(context.Context) (sessionConn, error) {
		return slow, nil
	})
	client.connectBudget = 40 * time.Millisecond
	client.h3Cooldown = time.Minute
	require.True(t, quicIncluded)

	ctx := testContext(t)
	fast, err := client.DialContext(ctx, fallbackDestination)
	require.NoError(t, err)
	defer fast.Close()

	client.access.Lock()
	session := client.session
	client.access.Unlock()
	require.NotNil(t, session)

	// The carrier keeps delivering on another stream while the slow CONNECT waits.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(3 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				session.markReceived()
			}
		}
	}()

	_, err = client.DialContext(ctx, M.ParseSocksaddr("slow.test:443"))
	require.Error(t, err)
	require.ErrorIs(t, err, errConnectEstablishTimeout)
	require.NotErrorIs(t, err, errCarrierRetired,
		"a single slow target must not be reported as a retired carrier")
	require.EqualValues(t, 1, slow.slow.Load(), "the slow CONNECT was retried instead of reported")

	client.access.Lock()
	require.Same(t, session, client.session, "a receiving carrier was retired for one slow target")
	client.access.Unlock()
	require.False(t, session.retiring.Load())
	require.False(t, client.h3Cordoned(), "one slow target must not keep HTTP/3 off the node")
	require.False(t, slow.closed.Load())
}

func TestSharedCreationBudgetReleasesThePermit(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			started := make(chan struct{}, 1)
			client := newSharedOfferClient(t, protocol, func(ctx context.Context) (sessionConn, error) {
				started <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			})
			client.creationBudget = 60 * time.Millisecond
			var finishes atomic.Int32
			client.admitCreation = func(ctx context.Context) (context.Context, func(error), error) {
				return ctx, func(error) { finishes.Add(1) }, nil
			}

			start := time.Now()
			_, err := client.DialContext(testContext(t), holdDestination())
			require.Error(t, err)
			require.Less(t, time.Since(start), 3*time.Second, "the shared creation outlived its budget")
			require.Eventually(t, func() bool { return finishes.Load() == 1 }, 2*time.Second, 5*time.Millisecond,
				"the admission permit of the timed-out creation was not released")
			client.access.Lock()
			require.Nil(t, client.pending, "the timed-out creation stayed published")
			client.access.Unlock()

			// The client is usable after the budget: a new creation is started, not poisoned.
			select {
			case <-started:
			default:
			}
			_, err = client.DialContext(testContext(t), holdDestination())
			require.Error(t, err)
			require.Eventually(t, func() bool { return finishes.Load() == 2 }, 2*time.Second, 5*time.Millisecond)
			require.EqualValues(t, 2, finishes.Load())
		})
	}
}

func TestArmedVerdictRetiresAPublishedHTTP3Session(t *testing.T) {
	require.True(t, quicIncluded)
	h3 := &selectiveSession{}
	var h2Calls atomic.Int32
	h2 := newFakeSession()
	client, _ := newFallbackTestClient(t,
		func(context.Context) (sessionConn, error) { return h3, nil },
		func(context.Context) (sessionConn, error) { h2Calls.Add(1); return h2, nil },
	)
	client.h3Cooldown = time.Minute

	ctx := testContext(t)
	first, err := client.DialContext(ctx, fallbackDestination)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	// A failure that was already in flight arms the verdict after the session was published. The
	// next demand must not keep reusing that HTTP/3 session.
	client.cordonNow("test race")
	second, err := client.DialContext(ctx, fallbackDestination)
	require.NoError(t, err)
	require.NoError(t, second.Close())
	require.Eventually(t, func() bool { return h3.closed.Load() }, 2*time.Second, 5*time.Millisecond,
		"the published HTTP/3 session was reused while the HTTP/2 verdict was open")
	require.EqualValues(t, 1, h2Calls.Load())
}
