//go:build with_quic

package http

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The configuration half of the H3 -> H2 fallback.
//
// `tls.alpn` is the list of protocols THIS client offers. Empty means "derive per version": the
// TCP dialer gets h2 + http/1.1 and the QUIC configuration gets h3. An explicit list is used on
// both sides, so `alpn: ["h3"]` leaves the TCP side with nothing it could negotiate. The client
// keeps that constraint (widening it would change what goes on the wire against an explicit
// configuration) and reports why the fallback cannot exist, instead of surfacing the server's
// no_application_protocol alert from a handshake that could never have succeeded.

func TestTCPFallbackDiagnosticSelection(t *testing.T) {
	cases := []struct {
		name      string
		tls       option.OutboundTLSOptions
		isDefault bool
		want      bool
	}{
		{"empty alpn is derived per version", option.OutboundTLSOptions{Enabled: true}, true, false},
		{"explicit h3 only", option.OutboundTLSOptions{Enabled: true, ALPN: []string{"h3"}}, false, true},
		{"explicit h3 + h2", option.OutboundTLSOptions{Enabled: true, ALPN: []string{"h3", "h2"}}, false, false},
		{"explicit h3 + http/1.1", option.OutboundTLSOptions{Enabled: true, ALPN: []string{"h3", "http/1.1"}}, false, false},
		{"explicit h2 only", option.OutboundTLSOptions{Enabled: true, ALPN: []string{"h2"}}, false, false},
		{"tls disabled", option.OutboundTLSOptions{}, false, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			diagnostic := tcpFallbackDiagnostic(testCase.tls, testCase.isDefault)
			require.Equal(t, testCase.want, diagnostic != "", "diagnostic: %q", diagnostic)
			if testCase.want {
				require.Contains(t, diagnostic, "tls.alpn")
			}
		})
	}
}

// newALPNTestClient goes through the production constructor, so the ALPN defaulting under test is
// the real one and not a re-implementation in the test.
func newALPNTestClient(t *testing.T, alpn []string, outboundDialer *recordingDialer) *Client {
	t.Helper()
	client, err := NewClientWithTLS(context.Background(), logger.NOP(), outboundDialer,
		option.ServerOptions{Server: "127.0.0.1", ServerPort: 1},
		option.OutboundTLSOptions{Enabled: true, ServerName: "example.test", Insecure: true, ALPN: alpn},
		ClientOptions{Version: 3})
	require.NoError(t, err)
	return client
}

// TestH3OnlyALPNReportsTheUnavailableTCPFallback is the finding.
//
// The H3 attempt fails (blackholed UDP) and the client is about to try TCP, where the configured
// ALPN list offers nothing the server can select. Before this change the caller received a TLS
// error from that attempt; the error now names the configuration that makes the fallback
// impossible, and no TCP handshake is started for it.
func TestH3OnlyALPNReportsTheUnavailableTCPFallback(t *testing.T) {
	previous := http3EstablishTimeout
	http3EstablishTimeout = 20 * time.Millisecond
	t.Cleanup(func() { http3EstablishTimeout = previous })

	outboundDialer := &recordingDialer{err: errTestNoConn}
	client := newALPNTestClient(t, []string{"h3"}, outboundDialer)
	client.http3 = &hangingHTTP3Client{}

	_, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:443"))
	require.ErrorContains(t, err, "TCP fallback is unavailable")
	require.ErrorContains(t, err, "tls.alpn")
	require.Zero(t, outboundDialer.calls,
		"no TCP handshake may be started when the configuration cannot negotiate one")
}

// TestDerivedALPNStillFallsBack is the positive control: the diagnostic must not fire, and must not
// stop the fallback, for the configuration a normal user has (no explicit tls.alpn).
func TestDerivedALPNStillFallsBack(t *testing.T) {
	previous := http3EstablishTimeout
	http3EstablishTimeout = 20 * time.Millisecond
	t.Cleanup(func() { http3EstablishTimeout = previous })

	outboundDialer := &recordingDialer{err: errTestNoConn}
	client := newALPNTestClient(t, nil, outboundDialer)
	client.http3 = &hangingHTTP3Client{}

	_, err := client.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:443"))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "TCP fallback is unavailable")
	require.NotZero(t, outboundDialer.calls,
		"an empty tls.alpn must still reach the TCP fallback")
}
