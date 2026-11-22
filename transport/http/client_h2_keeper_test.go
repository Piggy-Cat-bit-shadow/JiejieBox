package http

// Repository test for G03's HTTP/2 idle keeper. The probe was originally run read-only by the
// independent reviewer; it is checked in here so the active-stream protection has a normal CI
// home. It establishes a real HTTP/2 connection to a local TLS server, holds an active response
// body, and proves CloseIdleConnections neither closes that connection nor the stream. It then
// proves an idle connection is removed. The shipped legacy profile uses the State-based idle
// predicate; the wrap profile shares the same pool method and differs only in idle().

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"github.com/stretchr/testify/require"
)

func TestHTTP2KeeperLeavesActiveStreamAndTrimsIdle(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		close(entered)
		<-release
		_, _ = writer.Write([]byte("done"))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)

	tlsConn, err := tls.Dial("tcp", server.Listener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2"},
	})
	require.NoError(t, err)
	clientConn, err := newHTTP2ClientConn(&http2.Transport{}, tlsConn)
	require.NoError(t, err)
	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	responseCh := make(chan *http.Response, 1)
	errorCh := make(chan error, 1)
	go func() {
		response, roundTripErr := clientConn.roundTrip(request)
		if roundTripErr != nil {
			errorCh <- roundTripErr
			return
		}
		responseCh <- response
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the server never saw the HTTP/2 request")
	}
	var response *http.Response
	select {
	case response = <-responseCh:
	case roundTripErr := <-errorCh:
		t.Fatal(roundTripErr)
	case <-time.After(5 * time.Second):
		t.Fatal("the HTTP/2 response headers never arrived")
	}
	require.False(t, clientConn.idle(), "a connection with an open response body is reported idle")
	client := &Client{http2Conns: []*http2ClientConn{clientConn}}
	client.CloseIdleConnections()
	require.Len(t, client.http2Conns, 1, "CloseIdleConnections dropped a connection carrying an active stream")
	close(release)
	_, err = response.Body.Read(make([]byte, 4))
	require.NoError(t, err, "the active stream died after CloseIdleConnections")
	require.NoError(t, response.Body.Close())
	deadline := time.Now().Add(3 * time.Second)
	for !clientConn.idle() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.True(t, clientConn.idle(), "the connection never became idle after the stream finished")
	client.CloseIdleConnections()
	require.Empty(t, client.http2Conns, "CloseIdleConnections left an idle connection pooled")
}
