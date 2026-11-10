package httpclient

import (
	"context"
	stdTLS "crypto/tls"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/quic-go"

	"github.com/stretchr/testify/require"
)

// Proving BOTH race legs dispatched needs a barrier at each transport, not the clone count:
// cloning two bodies happens before either goroutine starts. The H3 leg signals from its dial
// function; the H2 leg from a wrapper around the inner transport.

type barrierTransport struct {
	inner   innerTransport
	entered chan struct{}
	release chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newBarrierTransport(inner innerTransport) *barrierTransport {
	return &barrierTransport{
		inner:   inner,
		entered: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (t *barrierTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.once.Do(func() { close(t.entered) })
	<-t.release
	defer close(t.done)
	return t.inner.RoundTrip(request)
}

func (t *barrierTransport) CloseIdleConnections() { t.inner.CloseIdleConnections() }
func (t *barrierTransport) Close() error          { return t.inner.Close() }

func barrierH3Dial(address string) (func(context.Context, *stdTLS.Config, *quic.Config) (*quic.Conn, error), chan struct{}, chan struct{}) {
	entered := make(chan struct{})
	release := make(chan struct{})
	once := &sync.Once{}
	base := dialRetrySafetyH3(address)
	dial := func(ctx context.Context, tlsConfig *stdTLS.Config, quicConfig *quic.Config) (*quic.Conn, error) {
		once.Do(func() { close(entered) })
		<-release
		return base(ctx, tlsConfig, quicConfig)
	}
	return dial, entered, release
}

func waitFor(t *testing.T, channel chan struct{}, message string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}

func raceRequestForTest(t *testing.T, address string, first, second io.ReadCloser) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, "https://"+address+"/race", strings.NewReader("payload"))
	require.NoError(t, err)
	request.Header.Set("Idempotency-Key", "dispatch")
	clones := 0
	request.GetBody = func() (io.ReadCloser, error) {
		clones++
		if clones == 1 {
			return first, nil
		}
		return second, nil
	}
	return request
}

// TestRaceDispatchesBothLegsAndH3Wins: both transports are entered, the fast H3 leg wins, the
// loser's body is released and its transport call returns.
func TestRaceDispatchesBothLegsAndH3Wins(t *testing.T) {
	h3Server := startRetrySafetyH3Server(t, okHandler)
	h2Server, h2Transport := startRetrySafetyH2Server(t, func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(150 * time.Millisecond)
		okHandler(writer, request)
	})
	_ = h2Server
	dial, h3Entered, releaseH3 := barrierH3Dial(h3Server.address)
	h2Barrier := newBarrierTransport(h2Transport)
	transport := newRetrySafetyTransport(h2Barrier, dial, 0)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	winnerBody := &auditTrackedBody{Reader: strings.NewReader("payload")}
	loserBody := &auditTrackedBody{Reader: strings.NewReader("payload")}
	request := raceRequestForTest(t, h3Server.address, winnerBody, loserBody)

	type result struct {
		response *http.Response
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, err := transport.RoundTrip(request)
		done <- result{response: response, err: err}
	}()
	waitFor(t, h3Entered, "the H3 leg never reached its transport")
	waitFor(t, h2Barrier.entered, "the H2 leg never reached its transport")
	close(releaseH3)
	close(h2Barrier.release)

	select {
	case raceResult := <-done:
		require.NoError(t, raceResult.err)
		require.Equal(t, "HTTP/3.0", raceResult.response.Proto)
		_ = raceResult.response.Body.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("the race did not return")
	}
	waitFor(t, h2Barrier.done, "the cancelled H2 leg never returned from its transport")
	require.Eventually(t, func() bool { return winnerBody.closes.Load() == 1 }, time.Second, time.Millisecond)
	require.Eventually(t, func() bool { return loserBody.closes.Load() == 1 }, time.Second, time.Millisecond,
		"the cancelled loser's body must be released")
}

// TestRaceDispatchesBothLegsAndH2Wins: the H3 leg is slow, the H2 leg wins, and the loser's body is
// released without arming the H3 verdict.
func TestRaceDispatchesBothLegsAndH2Wins(t *testing.T) {
	h3Server := startRetrySafetyH3Server(t, func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(150 * time.Millisecond)
		okHandler(writer, request)
	})
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	_ = h2Server
	dial, h3Entered, releaseH3 := barrierH3Dial(h3Server.address)
	h2Barrier := newBarrierTransport(h2Transport)
	transport := newRetrySafetyTransport(h2Barrier, dial, 0)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	h3Body := &auditTrackedBody{Reader: strings.NewReader("payload")}
	h2Body := &auditTrackedBody{Reader: strings.NewReader("payload")}
	request := raceRequestForTest(t, h3Server.address, h3Body, h2Body)

	type result struct {
		response *http.Response
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, err := transport.RoundTrip(request)
		done <- result{response: response, err: err}
	}()
	waitFor(t, h3Entered, "the H3 leg never reached its transport")
	waitFor(t, h2Barrier.entered, "the H2 leg never reached its transport")
	close(releaseH3)
	close(h2Barrier.release)

	select {
	case raceResult := <-done:
		require.NoError(t, raceResult.err)
		require.Equal(t, "HTTP/2.0", raceResult.response.Proto)
		_ = raceResult.response.Body.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("the race did not return")
	}
	require.Eventually(t, func() bool { return h3Body.closes.Load() == 1 }, time.Second, time.Millisecond,
		"the cancelled H3 leg's body must be released")
	require.Eventually(t, func() bool { return h2Body.closes.Load() == 1 }, time.Second, time.Millisecond)

	transport.verdictAccess.Lock()
	broken := len(transport.broken)
	transport.verdictAccess.Unlock()
	require.Zero(t, broken, "a race-cancelled H3 leg must not arm the verdict")
}

// TestRaceSimultaneousCompletionsDeliverExactlyOneResponse: both legs are released together and
// both servers answer immediately, so the winner is whichever result the race loop sees first.
// Exactly one response may be delivered, the other leg's body must be released, and no cancellation
// may be remembered as a transport verdict.
func TestRaceSimultaneousCompletionsDeliverExactlyOneResponse(t *testing.T) {
	h3Server := startRetrySafetyH3Server(t, okHandler)
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	_ = h2Server
	dial, h3Entered, releaseH3 := barrierH3Dial(h3Server.address)
	h2Barrier := newBarrierTransport(h2Transport)
	transport := newRetrySafetyTransport(h2Barrier, dial, 0)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	h3Body := &auditTrackedBody{Reader: strings.NewReader("payload")}
	h2Body := &auditTrackedBody{Reader: strings.NewReader("payload")}
	request := raceRequestForTest(t, h3Server.address, h3Body, h2Body)

	type result struct {
		response *http.Response
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, err := transport.RoundTrip(request)
		done <- result{response: response, err: err}
	}()
	waitFor(t, h3Entered, "the H3 leg never reached its transport")
	waitFor(t, h2Barrier.entered, "the H2 leg never reached its transport")
	close(releaseH3)
	close(h2Barrier.release)

	var raceResult result
	select {
	case raceResult = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the race did not return")
	}
	require.NoError(t, raceResult.err)
	require.Contains(t, []string{"HTTP/2.0", "HTTP/3.0"}, raceResult.response.Proto)
	body, err := io.ReadAll(raceResult.response.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", string(body))
	_ = raceResult.response.Body.Close()

	require.Eventually(t, func() bool { return h3Body.closes.Load() == 1 }, time.Second, time.Millisecond)
	require.Eventually(t, func() bool { return h2Body.closes.Load() == 1 }, time.Second, time.Millisecond)
	transport.verdictAccess.Lock()
	broken := len(transport.broken)
	transport.verdictAccess.Unlock()
	require.Zero(t, broken)
}
