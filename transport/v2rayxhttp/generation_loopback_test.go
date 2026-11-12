package v2rayxhttp

// Q12: a REAL loopback for the XMUX pool's network-generation ownership.
//
// generation_test.go pins the wiring against the pool's fake; this file answers the question that
// fake cannot: with a real socket, a real HTTP/1.1 peer and the production client, does a demand in
// the transition window really open a NEW connection instead of being lent the pooled one, and is a
// dial that crossed the change really discarded?
//
// The peer is a minimal XHTTP stream-one server: one POST, 200 with flushed headers, then a
// full-duplex echo of the request body. That is enough to make the production dial path run end to
// end (request body, response body, full duplex) without a proxy, and the listener counts accepted
// TCP connections - which is the observation "the new generation did not reuse the old connection"
// is made from.

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// xhttpCountingListener counts accepted TCP connections, which is how "a new connection was really
// opened" is observed without reading pool internals.
type xhttpCountingListener struct {
	net.Listener
	accepted *atomic.Int32
}

func (l *xhttpCountingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.accepted.Add(1)
	return conn, nil
}

// newXHTTPEchoServer is a real stream-one peer. beforeHeaders, when set, runs ONCE before the
// response headers are written, which is what lets a test hold the dial open on a real socket.
func newXHTTPEchoServer(t *testing.T, beforeHeaders func()) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	accepted := new(atomic.Int32)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if beforeHeaders != nil {
			beforeHeaders()
		}
		// Xray's stream-one endpoint reads the uplink while it writes the downlink.
		require.NoError(t, http.NewResponseController(writer).EnableFullDuplex())
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		buffer := make([]byte, 64)
		for {
			read, readErr := request.Body.Read(buffer)
			if read > 0 {
				if _, writeErr := writer.Write(buffer[:read]); writeErr != nil {
					return
				}
				writer.(http.Flusher).Flush()
			}
			if readErr != nil {
				return
			}
		}
	}))
	server.Listener = &xhttpCountingListener{Listener: server.Listener, accepted: accepted}
	server.Start()
	t.Cleanup(server.Close)
	return server, accepted
}

// newXHTTPLoopbackClient builds the production client (stream-one, cleartext HTTP/1.1 - the version
// this transport selects without TLS) carrying the coordinator.
func newXHTTPLoopbackClient(t *testing.T, coordinator *runtimecoord.Coordinator, address string) *Client {
	t.Helper()
	ctx := service.ContextWith[*runtimecoord.Coordinator](context.Background(), coordinator)
	transport, err := NewClient(ctx, N.SystemDialer, M.ParseSocksaddr(address), option.V2RayXHTTPOptions{
		Mode: modeStreamOne,
		Path: "/xhttp/",
	}, nil)
	require.NoError(t, err)
	client, ok := transport.(*Client)
	require.True(t, ok)
	require.NotNil(t, client.registration, "a client built with a coordinator must own its registration")
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// echoOverXHTTP writes payload and reads it back from the peer.
func echoOverXHTTP(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))
	_, err := conn.Write([]byte(payload))
	require.NoError(t, err)
	buffer := make([]byte, len(payload))
	_, err = io.ReadFull(conn, buffer)
	require.NoError(t, err)
	require.Equal(t, payload, string(buffer))
}

// TestAnXHTTPLoopbackDialDoesNotLendThePreviousGeneration is the audit's counterexample on a real
// socket: the pooled connection is idle, a generation is published, and the next demand must dial a
// new TCP connection instead of being handed the old one.
func TestAnXHTTPLoopbackDialDoesNotLendThePreviousGeneration(t *testing.T) {
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	server, accepted := newXHTTPEchoServer(t, nil)
	client := newXHTTPLoopbackClient(t, coordinator, server.Listener.Addr().String())

	first, err := client.DialContext(context.Background())
	require.NoError(t, err)
	echoOverXHTTP(t, first, "ping")
	require.NoError(t, first.Close())
	require.Eventually(t, func() bool { return accepted.Load() == 1 }, 10*time.Second, 5*time.Millisecond)

	coordinator.Advance(1)

	second, err := client.DialContext(context.Background())
	require.NoError(t, err)
	echoOverXHTTP(t, second, "pong")
	require.NoError(t, second.Close())
	require.Equal(t, int32(2), accepted.Load(),
		"a demand in the transition window must open a new connection, not reuse the pooled one")
	require.False(t, client.generationStale(), "a completed dial on the new generation acknowledges it")

	ledger := client.ResidencyLedger()
	require.Equal(t, "connection", string(ledger.Unit))
	require.GreaterOrEqual(t, ledger.Generation, uint64(1), "the pool generation moved with the network")
}

// TestAnXHTTPLoopbackDialDiscardsALateResult holds the peer's response headers back and publishes a
// new generation while the stream is in flight. The raise returns on body adoption, so the dial hands
// back a conn - and the response, when it arrives, must be DISCARDED: the caller's first read fails
// with the retryable stale error instead of carrying a stream built on the path that just ended.
func TestAnXHTTPLoopbackDialDiscardsALateResult(t *testing.T) {
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server, _ := newXHTTPEchoServer(t, func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	})
	client := newXHTTPLoopbackClient(t, coordinator, server.Listener.Addr().String())

	conn, err := client.DialContext(context.Background())
	require.NoError(t, err, "the raise settles on body adoption, before the response")
	<-entered
	coordinator.Advance(1)
	close(release)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	_, readErr := conn.Read(make([]byte, 4))
	require.Error(t, readErr, "a response from the previous generation must be discarded, not served")
	require.Contains(t, readErr.Error(), "network changed")
	require.True(t, client.generationStale(),
		"a discarded response must not acknowledge the generation it belongs to")
	_ = conn.Close()
}
