package v2rayxhttp

import (
	"context"
	"net"
	"time"

	"github.com/sagernet/sing-box/common/tls"
	M "github.com/sagernet/sing/common/metadata"

	"golang.org/x/net/http2"
)

// lx: XHTTP Round 2 — HTTP/2 keep-alive parity with the reference.
//
// Xray maps xmux.h_keep_alive_period onto http2.Transport.ReadIdleTimeout with
// two substitutions (transport/internet/splithttp/dialer.go:248-259):
//
//	0  -> net.ChromeH2KeepAlivePeriod, which Xray documents as 45 s
//	      ("consistent with chrome", common/net/net.go)
//	<0 -> 0, pings off
//	>0 -> as configured
//
// This fork passed the value straight through, so 0 — what a node has when it
// never set the field — silently turned HTTP/2 keep-alive pings off while the
// reference kept sending one every 45 s. The fork's own option documentation
// already promised the reference's behaviour ("Zero selects the default"), so
// this is a defect against its own contract rather than a tuning choice: a
// pooled connection that idles through a NAT or a CDN edge is dropped without
// either side noticing until the next stream needs it.
//
// HTTP/3 already had the equivalent rule and a test for it
// (http3KeepAlivePeriod, TestHTTP3KeepAlivePeriod): Xray's
// net.QuicgoH3KeepAlivePeriod is 10 s and this fork's constant is the same.
const http2DefaultKeepAlivePeriod = 45 * time.Second

// http2KeepAlivePeriod normalizes xmux.h_keep_alive_period for http2.Transport.
func http2KeepAlivePeriod(period time.Duration) time.Duration {
	switch {
	case period == 0:
		return http2DefaultKeepAlivePeriod
	case period < 0:
		return 0
	default:
		return period
	}
}

// newHTTP2Transport builds one pooled HTTP/2 connection, with the keep-alive
// period normalized the way the reference normalizes it. It mirrors
// newHTTP1Transport and newHTTP3Transport: the version-specific transport is
// built in one place, so a test can assert the exact configuration a node gets.
//
// IdleConnTimeout is deliberately left at its zero value (no limit): the
// reference sets it to net.ConnIdleTimeout (300 s), while this fork lets XMUX's
// own h_max_reusable_secs (1800-3000 s by default) decide when a pooled
// connection is retired. Keeping connections longer is not a reconnect storm;
// the measured connection-setup CPU in the packet-up work came from the write
// path, not from re-dials.
func newHTTP2Transport(tlsDialer tls.Dialer, keepAlivePeriod time.Duration) *http2.Transport {
	return &http2.Transport{
		ReadIdleTimeout: http2KeepAlivePeriod(keepAlivePeriod),
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.STDConfig) (net.Conn, error) {
			return tlsDialer.DialTLSContext(ctx, M.ParseSocksaddr(addr))
		},
	}
}
