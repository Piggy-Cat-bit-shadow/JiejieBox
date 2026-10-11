package box_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// svHTTPClientDemandFixture builds a box whose http_client dials through an outbound with a given
// advertised network set. The `direct` outbound is declared because `route.final` names it.
func svHTTPClientDemandFixture(client string, detourOutbound string) string {
	return `{
  "log": {"disabled": true},
  "http_clients": [` + client + `],
  "outbounds": [` + detourOutbound + `, {"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"}
}`
}

const (
	svTCPOnlyDetour = `{"type": "socks", "tag": "d", "server": "127.0.0.1", "server_port": 1080,
                        "version": "5", "network": "tcp"}`
	svUDPOnlyDetour = `{"type": "socks", "tag": "d", "server": "127.0.0.1", "server_port": 1080,
                        "version": "5", "network": "udp"}`
	svDualDetour = `{"type": "socks", "tag": "d", "server": "127.0.0.1", "server_port": 1080,
                     "version": "5"}`
)

// TestAnHTTPClientDetourMustCarryTheConfiguredTransports is the consumer-demand closure.
//
// # Why the requirement comes from the version, not the protocol label
//
// `version: 3` builds H3 with an H2 fallback unless `disable_version_fallback` is set, and that
// fallback is taken on a non-https URL, on a websocket upgrade, and on any H3 failure
// (http3_transport.go's RoundTrip). So the same version needs UDP **and** TCP, or UDP **alone** -
// and an HTTP/1-or-2 client needs TCP. A rule that said "an HTTP client needs TCP" would be wrong in
// the first case, and would refuse a UDP-only detour that works.
//
// Each refusal below is a case the runtime provably cannot serve; each acceptance is the twin that
// keeps the check from being "refuse every detour".
func TestAnHTTPClientDetourMustCarryTheConfiguredTransports(t *testing.T) {
	t.Parallel()

	accepted := map[string]struct {
		client string
		detour string
		why    string
	}{
		"forced H3 over a UDP detour": {
			client: `{"tag": "hc", "version": 3, "disable_version_fallback": true, "detour": "d"}`,
			detour: svUDPOnlyDetour,
			why:    "H3 alone is UDP, so a UDP detour serves every request",
		},
		"HTTP/1 or 2 over a TCP detour": {
			client: `{"tag": "hc", "version": 2, "detour": "d"}`,
			detour: svTCPOnlyDetour,
			why:    "helpers.go:28 is the single TLS dial, and it is TCP",
		},
		"H3 with its fallback over a dual detour": {
			client: `{"tag": "hc", "version": 3, "detour": "d"}`,
			detour: svDualDetour,
			why:    "both transports are reachable, and the detour carries both",
		},
		"no detour at all": {
			client: `{"tag": "hc", "version": 2}`,
			detour: svTCPOnlyDetour,
			why:    "a client with no detour is the ordinary case and must stay lazy",
		},
		// MOVED here from the refused set under the H-PROVABLE contract. It was asserted as a
		// refusal, and that refusal WAS the false deny: `http3FallbackTransport` races the H3 attempt
		// against an H2 one and fails only when BOTH fail (http3_transport.go:252-258), so the H2
		// side serves every request over a TCP-only detour. The client works; it has no HTTP/3.
		"H3 with its fallback over a TCP-only detour": {
			client: `{"tag": "hc", "version": 3, "detour": "d"}`,
			detour: svTCPOnlyDetour,
			why: "the H2 fallback side of the race serves the request, so only the HTTP/3 part is " +
				"unavailable - not a reason to refuse a working client",
		},
	}
	for name, testCase := range accepted {
		name, testCase := name, testCase
		t.Run("accepted: "+name, func(t *testing.T) {
			t.Parallel()
			instance, err := newBoxFromConfig(t, svHTTPClientDemandFixture(testCase.client, testCase.detour))
			require.NoError(t, err)
			t.Cleanup(func() { _ = instance.Close() })
			require.NoError(t, instance.Start(), testCase.why)
		})
	}

	refused := map[string]struct {
		client string
		detour string
		why    string
	}{
		"forced H3 over a TCP-only detour": {
			client: `{"tag": "hc", "version": 3, "disable_version_fallback": true, "detour": "d"}`,
			detour: svTCPOnlyDetour,
			why:    "H3 alone is UDP and the flag removed every fallback, so no request can be made",
		},
		"HTTP/1 or 2 over a UDP-only detour": {
			client: `{"tag": "hc", "version": 2, "detour": "d"}`,
			detour: svUDPOnlyDetour,
			why:    "the TLS dial is TCP and there is no UDP path in an H1/H2 transport",
		},
		"a detour naming nothing": {
			client: `{"tag": "hc", "version": 2, "detour": "ghost"}`,
			detour: svTCPOnlyDetour,
			why:    "the reference check, which this must not have replaced",
		},
	}
	for name, testCase := range refused {
		name, testCase := name, testCase
		t.Run("refused: "+name, func(t *testing.T) {
			t.Parallel()
			instance, err := newBoxFromConfig(t, svHTTPClientDemandFixture(testCase.client, testCase.detour))
			require.NoError(t, err)
			t.Cleanup(func() { _ = instance.Close() })
			startErr := instance.Start()
			require.Error(t, startErr, testCase.why)
		})
	}
}

// TestAnHTTPClientDetourToAnEndpointStillStarts is the cross-namespace control.
//
// The detour lookup falls back to the endpoint namespace, so a client dialling through an endpoint
// must be judged on the endpoint's advertisement. It publishes {tcp,udp,icmp}, so an HTTP client is
// served - and a check that only consulted the outbound map would either miss the object or refuse a
// working configuration.
func TestAnHTTPClientDetourToAnEndpointStillStarts(t *testing.T) {
	t.Parallel()

	instance, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "http_clients": [{"tag": "hc", "version": 2, "detour": "wg-ep"}],
  "endpoints": [{"type": "wireguard", "tag": "wg-ep", "system": false,
    "address": ["10.0.0.2/32"],
    "private_key": "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=",
    "peers": [{"address": "127.0.0.1", "port": 51820,
               "public_key": "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
               "allowed_ips": ["0.0.0.0/0"]}]}],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = instance.Close() })
	require.NoError(t, instance.Start(),
		"the endpoint namespace is a legal detour target and its advertisement serves TCP")
}
