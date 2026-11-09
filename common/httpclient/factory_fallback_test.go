//go:build with_quic

package httpclient

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/logger"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The production factory, real servers, one request.
//
// `newTransport` is what `ManagedTransport` builds for a Go-engine HTTP client: version 3 without
// `disable_version_fallback` produces the HTTP/3-with-HTTP/2-fallback transport this task fixed.
// This test enters through that factory, points it at a real HTTP/2 TLS server, and binds a UDP
// socket to the same port that never answers - the shape of a blackholed UDP path with a healthy
// TCP path. The request must be served by HTTP/2.
func TestTransportFactoryServesFromH2WhenUDPIsSilent(t *testing.T) {
	nonce := "factory-probe-7c1f"
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Served-By", "h2")
		_, _ = io.WriteString(writer, "h2:"+request.Header.Get("X-Nonce"))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	host, portString, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portString)
	require.NoError(t, err)

	// The UDP side of the same authority: it reads and discards, so no QUIC handshake can ever
	// complete.
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(host), Port: port})
	require.NoError(t, err)
	defer udpConn.Close()
	go func() {
		buffer := make([]byte, 4096)
		for {
			if _, _, readErr := udpConn.ReadFromUDP(buffer); readErr != nil {
				return
			}
		}
	}()

	previousBudget := h3EstablishBudget
	h3EstablishBudget = 150 * time.Millisecond
	t.Cleanup(func() { h3EstablishBudget = previousBudget })

	address := net.JoinHostPort(host, portString)
	baseTLSConfig, err := tls.NewClient(context.Background(), logger.NOP(), address, option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: host,
		Insecure:   true,
	})
	require.NoError(t, err)

	transport, err := newTransport(N.SystemDialer, baseTLSConfig, option.HTTPClientOptions{
		Version: 3,
		DialerOptions: option.DialerOptions{
			AbstractDialerOptions: option.AbstractDialerOptions{
				FallbackDelay: badoption.Duration(20 * time.Millisecond),
			},
		},
	})
	require.NoError(t, err)
	defer transport.Close()

	request, err := http.NewRequest(http.MethodGet, "https://"+address+"/probe", nil)
	require.NoError(t, err)
	request.Header.Set("X-Nonce", nonce)

	start := time.Now()
	response, err := transport.RoundTrip(request)
	elapsed := time.Since(start)
	require.NoError(t, err, "the request must be served by the HTTP/2 fallback")
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "h2:"+nonce, string(body),
		"the bytes must come from the HTTP/2 server, carrying this request's nonce")
	require.Equal(t, "HTTP/2.0", response.Proto,
		"and the transport must have NEGOTIATED HTTP/2, not merely answered over some TCP path")
	require.Equal(t, "h2", response.Header.Get("X-Served-By"))
	require.Less(t, elapsed, 5*time.Second,
		"the fallback must not wait for the silent UDP path's own timeout")
	require.False(t, strings.Contains(response.Header.Get("X-Served-By"), "h3"))
}
