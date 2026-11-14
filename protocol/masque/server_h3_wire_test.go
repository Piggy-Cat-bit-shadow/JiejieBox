//go:build with_quic

package masque

// The one thing this package never did: put a real HTTP/3 request on the wire against a started
// MASQUE server endpoint.
//
// The endpoint tests construct endpoints and pin their state machines; the H3 listener itself was
// only ever asserted to exist. That gap matters for the carrier matrix, because "the CONNECT-IP row
// is missing a datapath fixture" and "the server endpoint does not serve HTTP/3 at all" are very
// different diagnoses - and only a wire request tells them apart.
//
// It serves. A plain HTTP/3 GET with the endpoint's certificate reaches the endpoint's HTTP server,
// which answers 407 with the proxy authentication challenge - i.e. the QUIC listener, the TLS
// handshake, the H3 server and the authentication layer are all on the path, and an unauthenticated
// request is refused BEFORE any tunnel is considered.
//
// This also locates the remaining CONNECT-IP gap precisely: the client endpoint's session loop is
// what does not establish (see the handoff), not the server side.

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

func TestAServerEndpointAnswersAnUnauthenticatedHTTP3RequestWithAChallenge(t *testing.T) {
	requireHTTP3Client(t)
	runUnauthenticatedH3Request(t, option.QUICOptions{})
}

// TestAServerEndpointServesHTTP3WithCongestionControl pins the runtime path of
// quic_congestion_control on this endpoint: the field used to be decoded and
// ignored. The request proves the listener still starts and serves with the
// option set; the sender itself is asserted in
// transport/http.TestServerCongestionControlIsAppliedOnHandshake.
func TestAServerEndpointServesHTTP3WithCongestionControl(t *testing.T) {
	requireHTTP3Client(t)
	runUnauthenticatedH3Request(t, option.QUICOptions{CongestionControl: "cubic"})
}

// TestServerEndpointRejectsUnknownCongestionControl proves the construction-time
// validation `sing-box check` relies on. It runs before any listener exists, so
// no network context is needed beyond the TLS options that HTTP/3 requires.
func TestServerEndpointRejectsUnknownCongestionControl(t *testing.T) {
	_, err := NewServerEndpoint(context.Background(), nil, log.NewNOPFactory().Logger(), "masque-server",
		option.MASQUEServerEndpointOptions{
			MASQUEEndpointOptions: option.MASQUEEndpointOptions{MTU: 1500},
			Version:               badoption.Listable[int]{3},
			HTTP3Options:          option.QUICOptions{CongestionControl: "BROKEN"},
			InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
				TLS: &option.InboundTLSOptions{Enabled: true},
			},
		})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown quic_congestion_control")
}

func runUnauthenticatedH3Request(t *testing.T, quicOptions option.QUICOptions) {
	t.Helper()
	logger := log.NewNOPFactory().Logger()
	certPEM, keyPEM := newLoopbackCertificate(t)

	serverContext := service.ContextWithDefaultRegistry(context.Background())
	serverContext = service.ContextWith[adapter.NetworkManager](serverContext, &stubNetworkManager{})
	created, err := NewServerEndpoint(serverContext, nil, logger, "masque-server",
		option.MASQUEServerEndpointOptions{
			MASQUEEndpointOptions: option.MASQUEEndpointOptions{MTU: 1500},
			ListenOptions: option.ListenOptions{
				Listen:     common.Ptr(badoption.Addr(netip.AddrFrom4([4]byte{127, 0, 0, 1}))),
				ListenPort: 0,
			},
			Version:      badoption.Listable[int]{3},
			Address:      badoption.Listable[netip.Prefix]{netip.MustParsePrefix("10.42.0.0/24")},
			Path:         "/",
			HTTP3Options: quicOptions,
			Users:        []auth.User{{Username: "carrier", Password: "carrier-password"}},
			InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
				TLS: &option.InboundTLSOptions{
					Enabled:     true,
					ServerName:  "example.test",
					Certificate: []string{certPEM},
					Key:         badoption.Listable[string]{keyPEM},
				},
			},
		})
	require.NoError(t, err)
	serverEndpoint, isServer := created.(*ServerEndpoint)
	require.True(t, isServer)
	serverScope := adapter.NewScope(context.Background(), logger)
	require.NoError(t, serverEndpoint.Start(adapter.StartStateInitialize, serverScope))
	require.NoError(t, serverEndpoint.Start(adapter.StartStateStart, serverScope))
	t.Cleanup(func() { _ = serverScope.Close() })

	address := serverEndpoint.listener.UDPConn().LocalAddr().(*net.UDPAddr)
	transport := &http3.Transport{
		TLSClientConfig: &tls.Config{
			// The certificate is generated per test for example.test; the handshake is real, the
			// trust decision is the test's, and nothing else in the path is skipped.
			InsecureSkipVerify: true,
			ServerName:         "example.test",
			NextProtos:         []string{"h3"},
		},
	}
	t.Cleanup(func() { _ = transport.Close() })
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	request, err := http.NewRequest(http.MethodGet, "https://"+address.String()+"/", nil)
	require.NoError(t, err)
	response, err := client.Do(request)
	require.NoError(t, err, "the endpoint's HTTP/3 listener did not answer a real request")
	defer response.Body.Close()

	require.Equal(t, http.StatusProxyAuthRequired, response.StatusCode,
		"an unauthenticated request must be refused with the proxy authentication challenge, got %s",
		response.Status)
	require.NotEmpty(t, response.Header.Get("Proxy-Authenticate"),
		"the refusal must carry a Proxy-Authenticate challenge for the client to answer")
	t.Logf("H3 request reached the endpoint and was refused with %s / %s",
		response.Status, response.Header.Get("Proxy-Authenticate"))
}
