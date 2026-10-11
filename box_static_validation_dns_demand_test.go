package box_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	svDNSDirect  = `{"type": "direct", "tag": "direct"}`
	svDNSTCPOnly = `{"type": "socks", "tag": "d", "server": "127.0.0.1", "server_port": 1080,
                     "version": "5", "network": "tcp"}`
	svDNSUDPOnly = `{"type": "socks", "tag": "d", "server": "127.0.0.1", "server_port": 1080,
                     "version": "5", "network": "udp"}`
	svDNSDual = `{"type": "socks", "tag": "d", "server": "127.0.0.1", "server_port": 1080,
                  "version": "5"}`
)

// svDNSDemandFixture builds a box whose DNS transport dials through a detour with a known
// advertisement.
func svDNSDemandFixture(server string) string {
	return `{
  "log": {"disabled": true},
  "dns": {"servers": [` + server + `]},
  "outbounds": [{"type": "socks", "tag": "d", "server": "127.0.0.1", "server_port": 1080,
                 "version": "5", "network": "tcp"}, ` + svDNSDirect + `],
  "route": {"final": "direct"}
}`
}

// TestADNSTransportDetourMustCarryTheQuery pins each DNS transport's demand against the
// implementation that decides it.
//
// The two entries a name-based guess gets wrong are the ones worth reading twice:
//
//   - `udp` needs UDP **and** TCP, because a truncated response is retried over TCP. "UDP DNS needs
//     UDP" would bless a detour that breaks on any response too large for a datagram - and D4b below
//     is exactly that detour.
//   - `https` needs TCP **only**. I first modelled an optional H3 path for DoH and the implementation
//     refuted it: https_transport.go dials N.NetworkTCP at :37, :54 and :61 with no QUIC branch, and
//     `h3` is a SEPARATE type. D5 is that control.
func TestADNSTransportDetourMustCarryTheQuery(t *testing.T) {
	t.Parallel()

	accepted := map[string]struct {
		detour string
		why    string
	}{
		"quic (DoQ) over a UDP detour": {
			detour: svDNSUDPOnly,
			why:    "DoQ is QUIC, so UDP serves every query",
		},
		"h3 (DoH3) over a UDP detour": {
			detour: svDNSUDPOnly,
			why:    "DoH3 is HTTP/3 over QUIC",
		},
		"https (DoH) over a TCP detour": {
			detour: svDNSTCPOnly,
			why:    "DoH rides TCP and only TCP - `h3` is a separate transport type",
		},
		"udp over a dual detour": {
			detour: svDNSDual,
			why:    "the query and its truncation retry are both served",
		},
		"udp over a UDP-only detour": {
			detour: svDNSUDPOnly,
			why: "an ordinary response completes over UDP alone; only the truncation retry is " +
				"lost, which is a capability gap rather than an unusable resolver",
		},
	}
	for name, testCase := range accepted {
		name, testCase := name, testCase
		t.Run("accepted: "+name, func(t *testing.T) {
			t.Parallel()
			transportType := svTransportTypeFor(name)
			config := `{
  "log": {"disabled": true},
  "dns": {"servers": [{"tag": "d", "type": "` + transportType + `", "server": "8.8.8.8",
                       "detour": "d"}]},
  "outbounds": [` + testCase.detour + `, ` + svDNSDirect + `],
  "route": {"final": "direct"}
}`
			instance, err := newBoxFromConfig(t, config)
			require.NoError(t, err)
			t.Cleanup(func() { _ = instance.Close() })
			require.NoError(t, instance.Start(), testCase.why)
		})
	}

	refused := map[string]struct {
		transportType string
		detour        string
		why           string
	}{
		"quic (DoQ) over a TCP detour": {
			transportType: "quic",
			detour:        svDNSTCPOnly,
			why:           "QUIC has no TCP path",
		},
		"h3 (DoH3) over a TCP detour": {
			transportType: "h3",
			detour:        svDNSTCPOnly,
			why:           "HTTP/3 is QUIC",
		},
		"udp over a TCP detour": {
			transportType: "udp",
			detour:        svDNSTCPOnly,
			why:           "the query itself goes out over UDP",
		},
		// REMOVED as a refusal under the H-PROVABLE contract; it is in the ACCEPTED set above. The
		// old reason was "the TRUNCATION RETRY needs TCP, so a UDP-only detour breaks on any
		// response too large for a datagram" - true about the retry and false about the resolver,
		// which answers every ordinary query. A retry that MAY happen does not make the initial
		// transport unusable without the retry network.
	}
	for name, testCase := range refused {
		name, testCase := name, testCase
		t.Run("refused: "+name, func(t *testing.T) {
			t.Parallel()
			config := `{
  "log": {"disabled": true},
  "dns": {"servers": [{"tag": "d", "type": "` + testCase.transportType + `", "server": "8.8.8.8",
                       "detour": "d"}]},
  "outbounds": [` + testCase.detour + `, ` + svDNSDirect + `],
  "route": {"final": "direct"}
}`
			instance, err := newBoxFromConfig(t, config)
			require.NoError(t, err)
			t.Cleanup(func() { _ = instance.Close() })
			require.Error(t, instance.Start(), testCase.why)
		})
	}
}

// svTransportTypeFor maps an acceptance-case name to its DNS transport type.
func svTransportTypeFor(name string) string {
	switch {
	case len(name) >= 4 && name[:4] == "quic":
		return "quic"
	case len(name) >= 2 && name[:2] == "h3":
		return "h3"
	case len(name) >= 5 && name[:5] == "https":
		return "https"
	default:
		return "udp"
	}
}

// TestAnUnmodelledDNSTransportIsNotConstrained is the "leave it unmodelled rather than guess" rule.
//
// `local` does not reach the network through the outbound namespace a detour names, so it reports no
// requirement and a detour declared on it is not judged. Guessing a requirement here would refuse a
// configuration whose runtime path does not go through the object at all.
func TestAnUnmodelledDNSTransportIsNotConstrained(t *testing.T) {
	t.Parallel()

	instance, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "dns": {"servers": [{"tag": "d", "type": "local", "detour": "d"}]},
  "outbounds": [`+svDNSTCPOnly+`, `+svDNSDirect+`],
  "route": {"final": "direct"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = instance.Close() })
	require.NoError(t, instance.Start(),
		"local dials the system resolver, so a detour on it is not the thing carrying the query")
}
