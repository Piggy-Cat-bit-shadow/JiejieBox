//go:build with_quic

package httpclient

import (
	"context"
	stdTLS "crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"

	"github.com/stretchr/testify/require"
)

// Retry safety for the HTTP/3 -> HTTP/2 fallback.
//
// # The two defects these tests pin
//
//  1. EVERY request with a rebuildable body was raced, and the race starts the HTTP/2 leg while the
//     HTTP/3 leg may already have been sent. A POST is not safe to repeat, so a slow-but-successful
//     HTTP/3 response made the server execute the operation twice. "GetBody is not nil" answers
//     "can the body be rebuilt", not "may the operation be repeated".
//
//  2. The pool probe used `RoundTripOpt{OnlyCachedConn: true}`, and quic-go closes req.Body on
//     every error - including the miss, where no connection existed, nothing was read and nothing
//     was sent. A one-shot body (an io.Pipe, an upload reader with no GetBody) was destroyed before
//     the HTTP/2 retry could use it, so "UDP is blocked, fall back" failed on a closed body.
//
// Both tests drive the real transport against a real HTTP/3 server and a real HTTP/2 server, and
// assert on what the servers actually executed - not on an intermediate sentinel.

type retrySafetyServer struct {
	address  string
	requests atomic.Int64
	methods  []string
	bodies   []string
	access   sync.Mutex
}

func (s *retrySafetyServer) record(t *testing.T, request *http.Request) {
	t.Helper()
	body, _ := io.ReadAll(request.Body)
	s.access.Lock()
	s.methods = append(s.methods, request.Method)
	s.bodies = append(s.bodies, string(body))
	s.access.Unlock()
	s.requests.Add(1)
}

func (s *retrySafetyServer) executed() int {
	return int(s.requests.Load())
}

func (s *retrySafetyServer) receivedBodies() []string {
	s.access.Lock()
	defer s.access.Unlock()
	return append([]string(nil), s.bodies...)
}

// retrySafetyCertificate borrows httptest's generated self-signed certificate. The client side of
// every test sets InsecureSkipVerify, so the identity only has to be well-formed.
func retrySafetyCertificate(t *testing.T) stdTLS.Certificate {
	t.Helper()
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.StartTLS()
	certificate := server.TLS.Certificates[0]
	server.Close()
	return certificate
}

func startRetrySafetyH3Server(t *testing.T, handler http.HandlerFunc) *retrySafetyServer {
	t.Helper()
	server := &retrySafetyServer{}
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	server.address = packetConn.LocalAddr().String()

	h3Server := &http3.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			server.record(t, request)
			handler(writer, request)
		}),
		TLSConfig: &stdTLS.Config{
			Certificates: []stdTLS.Certificate{retrySafetyCertificate(t)},
			NextProtos:   []string{http3.NextProtoH3},
		},
		QUICConfig: &quic.Config{HandshakeIdleTimeout: 5 * time.Second},
	}
	go func() {
		_ = h3Server.Serve(packetConn)
	}()
	t.Cleanup(func() {
		_ = h3Server.Close()
		_ = packetConn.Close()
	})
	return server
}

func startRetrySafetyH2Server(t *testing.T, handler http.HandlerFunc) (*retrySafetyServer, innerTransport) {
	t.Helper()
	server := &retrySafetyServer{}
	httpServer := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		server.record(t, request)
		handler(writer, request)
	}))
	httpServer.EnableHTTP2 = true
	httpServer.StartTLS()
	t.Cleanup(httpServer.Close)
	server.address = httpServer.Listener.Addr().String()

	h2Address := server.address
	transport := &http.Transport{
		TLSClientConfig:   &stdTLS.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2: true,
		// The request URL names the HTTP/3 server's authority; this leg must reach the HTTP/2
		// endpoint regardless.
		DialContext: func(ctx context.Context, network string, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, h2Address)
		},
	}
	return server, &retrySafetyInner{transport: transport}
}

type retrySafetyInner struct {
	transport *http.Transport
}

func (t *retrySafetyInner) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.transport.RoundTrip(request)
}

func (t *retrySafetyInner) CloseIdleConnections() {
	t.transport.CloseIdleConnections()
}

func (t *retrySafetyInner) Close() error {
	t.transport.CloseIdleConnections()
	return nil
}

// dialRetrySafetyH3 is the real HTTP/3 leg: one UDP socket per connection, dialled to the test
// server's authority (the one the request URL names).
func dialRetrySafetyH3(address string) func(context.Context, *stdTLS.Config, *quic.Config) (*quic.Conn, error) {
	remote, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		panic(err)
	}
	return func(ctx context.Context, tlsConfig *stdTLS.Config, quicConfig *quic.Config) (*quic.Conn, error) {
		packetConn, listenErr := net.ListenUDP("udp", nil)
		if listenErr != nil {
			return nil, listenErr
		}
		conn, dialErr := quic.DialEarly(ctx, packetConn, remote, tlsConfig, quicConfig)
		if dialErr != nil {
			_ = packetConn.Close()
			return nil, dialErr
		}
		go func() {
			<-conn.Context().Done()
			_ = packetConn.Close()
		}()
		return conn, nil
	}
}

func newRetrySafetyTransport(h2 innerTransport, h3Dial func(context.Context, *stdTLS.Config, *quic.Config) (*quic.Conn, error), fallbackDelay time.Duration) *http3FallbackTransport {
	return &http3FallbackTransport{
		h3Transport: &http3.Transport{
			TLSClientConfig: &stdTLS.Config{InsecureSkipVerify: true},
			QUICConfig:      &quic.Config{HandshakeIdleTimeout: 2 * time.Second},
			Dial: func(ctx context.Context, _ string, tlsConfig *stdTLS.Config, quicConfig *quic.Config) (*quic.Conn, error) {
				return h3Dial(ctx, tlsConfig, quicConfig)
			},
		},
		h2Fallback:    h2,
		fallbackDelay: fallbackDelay,
		broken:        make(map[string]http3BrokenEntry),
	}
}

func okHandler(writer http.ResponseWriter, request *http.Request) {
	writer.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(writer, "ok")
}

func withH3EstablishBudget(t *testing.T, budget time.Duration) {
	t.Helper()
	previous := h3EstablishBudget
	h3EstablishBudget = budget
	t.Cleanup(func() { h3EstablishBudget = previous })
}

// TestRequestDoesNotWaitForAnotherRequestsDial is the pool half of A3.
//
// The HTTP/3 pool keys connections by host, and an entry whose dial is still running is still an
// entry. A request that arrives during that dial could therefore wait for the whole handshake -
// spending its own budget on another request's attempt - before its fallback even started. The
// probe now has the same window the race uses, and a probe that expires while the entry is still
// dialing sends THIS request to the fallback and leaves the dial alone.
func TestRequestDoesNotWaitForAnotherRequestsDial(t *testing.T) {
	dialEntered := make(chan struct{})
	release := make(chan struct{})
	var (
		enteredOnce sync.Once
		releaseOnce sync.Once
	)
	releaseDial := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseDial)

	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, func(ctx context.Context, _ *stdTLS.Config, _ *quic.Config) (*quic.Conn, error) {
		enteredOnce.Do(func() { close(dialEntered) })
		select {
		case <-release:
			return nil, errors.New("dial released")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, 30*time.Millisecond)

	// Request A opens the pool entry and holds its dial open.
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		request, err := http.NewRequest(http.MethodPost, "https://pool.example:443/first", strings.NewReader("first"))
		if err != nil {
			return
		}
		response, err := transport.RoundTrip(request)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	<-dialEntered

	// Request B arrives while A's dial is still running, with a caller budget far larger than
	// either window: only the probe window can end its wait.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	payload := "second"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://pool.example:443/second", strings.NewReader(payload))
	require.NoError(t, err)

	start := time.Now()
	response, err := transport.RoundTrip(request)
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Less(t, elapsed, time.Second,
		"a request must not wait for another request's dial; it has to reach its own fallback")
	require.Equal(t, []string{payload}, h2Server.receivedBodies(),
		"and it must reach HTTP/2 exactly once, with its body intact")
	require.False(t, transport.h3Broken("pool.example:443"),
		"waiting for someone else's dial is not evidence about the peer's HTTP/3")

	releaseDial()
	<-firstDone
}

// TestColdH3AttemptGivesUpWithinItsOwnBudget is A3 at the parent's level.
//
// A cold attempt can spend the CALLER's whole budget inside the HTTP/3 transport: the pool
// serialises dials per host, so this request may be waiting for a dial that belongs to another
// request, and the HTTP/2 fallback then has nothing left to spend. The establishment bound gives
// the attempt a budget of its own, so the fallback keeps the rest of the caller's.
//
// The bound is deliberately NOT a failure verdict: giving up on a slow handshake says nothing
// about whether the peer's HTTP/3 works, so the memory must stay unarmed and the next request is
// free to try HTTP/3 again.
func TestColdH3AttemptGivesUpWithinItsOwnBudget(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, func(ctx context.Context, _ *stdTLS.Config, _ *quic.Config) (*quic.Conn, error) {
		select {
		case <-release:
			return nil, errors.New("released")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, 20*time.Millisecond)
	withH3EstablishBudget(t, 50*time.Millisecond)

	payload := "post-payload"
	request, err := http.NewRequest(http.MethodPost, "https://stalled.example:443/submit", strings.NewReader(payload))
	require.NoError(t, err)

	start := time.Now()
	response, err := transport.RoundTrip(request)
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Less(t, elapsed, 3*time.Second,
		"the cold HTTP/3 attempt must give up inside its own budget instead of spending the caller's")
	require.Equal(t, []string{payload}, h2Server.receivedBodies())
	require.False(t, transport.h3Broken("stalled.example:443"),
		"our own establishment bound is not evidence that the peer's HTTP/3 is broken")
}

// TestNonIdempotentRequestIsNeverExecutedTwice is defect 1.
//
// The HTTP/3 leg is slower than the fallback delay but succeeds. A POST must be executed ONCE: the
// old race started the HTTP/2 leg while the HTTP/3 request was already on the wire, and the server
// executed both.
func TestNonIdempotentRequestIsNeverExecutedTwice(t *testing.T) {
	h3Server := startRetrySafetyH3Server(t, func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(200 * time.Millisecond)
		okHandler(writer, request)
	})
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, dialRetrySafetyH3(h3Server.address), 20*time.Millisecond)

	payload := `{"nonce":"7f3a"}`
	request, err := http.NewRequest(http.MethodPost, "https://"+h3Server.address+"/submit", strings.NewReader(payload))
	require.NoError(t, err)

	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	total := h3Server.executed() + h2Server.executed()
	require.Equal(t, 1, total,
		"a POST must be executed exactly once: HTTP/3 tried it, so the HTTP/2 leg may not repeat it")
	if total == 1 {
		require.Equal(t, []string{payload}, append(h3Server.receivedBodies(), h2Server.receivedBodies()...),
			"and the single execution must have carried the whole body")
	}
}

// TestNonIdempotentRequestFallsBackWhenQUICNeverConnects is the behaviour the fallback exists for:
// with the UDP path dead, a POST still reaches HTTP/2 - exactly once, with its body intact.
func TestNonIdempotentRequestFallsBackWhenQUICNeverConnects(t *testing.T) {
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, func(context.Context, *stdTLS.Config, *quic.Config) (*quic.Conn, error) {
		return nil, errors.New("udp is blocked")
	}, 20*time.Millisecond)

	payload := `{"nonce":"a1b2"}`
	request, err := http.NewRequest(http.MethodPost, "https://blocked.example:443/submit", strings.NewReader(payload))
	require.NoError(t, err)

	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	require.Equal(t, 1, h2Server.executed())
	require.Equal(t, []string{payload}, h2Server.receivedBodies())
	require.True(t, transport.h3Broken("blocked.example:443"),
		"a dead UDP path is a transport failure and must be remembered")
}

// TestOneShotRequestBodySurvivesThePoolProbe is defect 2.
//
// The body has no GetBody, so it cannot be rebuilt - it is a reader that is consumed once. The
// empty-pool probe used to close it, and the HTTP/3 attempt that followed then failed on a closed
// reader, on a perfectly healthy connection.
func TestOneShotRequestBodySurvivesThePoolProbe(t *testing.T) {
	h3Server := startRetrySafetyH3Server(t, okHandler)
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, dialRetrySafetyH3(h3Server.address), 20*time.Millisecond)

	payload := strings.Repeat("one-shot-body-", 8)
	reader, writer := io.Pipe()
	go func() {
		_, _ = io.WriteString(writer, payload)
		_ = writer.Close()
	}()
	request, err := http.NewRequest(http.MethodPost, "https://"+h3Server.address+"/upload", reader)
	require.NoError(t, err)
	require.Nil(t, request.GetBody, "precondition: this body cannot be rebuilt")

	response, err := transport.RoundTrip(request)
	require.NoError(t, err,
		"the empty-pool probe must not destroy a body it never read")
	require.NoError(t, response.Body.Close())
	require.Equal(t, []string{payload}, h3Server.receivedBodies(),
		"the HTTP/3 attempt must receive the complete one-shot body")
	require.Zero(t, h2Server.executed(), "the HTTP/3 connection was healthy, so HTTP/2 must not be involved")
}

// TestGetBodyFailureIsAnErrorNotAPanic is the shared helper's contract.
//
// GetBody is caller code and can fail. Inside the race it used to panic the whole process; here the
// same request must complete through the single-attempt path, because a single attempt only needs
// the original body.
func TestGetBodyFailureIsAnErrorNotAPanic(t *testing.T) {
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, func(context.Context, *stdTLS.Config, *quic.Config) (*quic.Conn, error) {
		return nil, errors.New("udp is blocked")
	}, 20*time.Millisecond)

	payload := "put-payload"
	request, err := http.NewRequest(http.MethodPut, "https://blocked.example:443/resource", strings.NewReader(payload))
	require.NoError(t, err)
	request.GetBody = func() (io.ReadCloser, error) {
		return nil, errors.New("this body cannot be rebuilt")
	}

	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, []string{payload}, h2Server.receivedBodies())
}
