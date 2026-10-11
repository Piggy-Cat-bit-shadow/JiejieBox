package box_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	svTrueFinalDirect  = `{"type": "direct", "tag": "direct"}`
	svTrueFinalTCPOnly = `{"type": "socks", "tag": "d", "server": "127.0.0.1", "server_port": 1080,
                          "version": "5", "network": "tcp"}`
	svTrueFinalUDPOnly = `{"type": "socks", "tag": "d", "server": "127.0.0.1", "server_port": 1080,
                          "version": "5", "network": "udp"}`
	svTrueFinalDual = `{"type": "socks", "tag": "d", "server": "127.0.0.1", "server_port": 1080,
                       "version": "5"}`
)

func svHTTPClientConfig(client, detour string) string {
	return `{"log": {"disabled": true}, "http_clients": [` + client + `],
  "outbounds": [` + detour + `, ` + svTrueFinalDirect + `], "route": {"final": "direct"}}`
}

func svDNSTransportConfig(transportType, detour string) string {
	return `{"log": {"disabled": true},
  "dns": {"servers": [{"tag": "s", "type": "` + transportType + `", "server": "8.8.8.8",
                       "detour": "d"}]},
  "outbounds": [` + detour + `, ` + svTrueFinalDirect + `], "route": {"final": "direct"}}`
}

// TestConsumerDemandContractIsProvableOnly pins the H-PROVABLE contract for BOTH consumer families,
// against the runtime semantics each one actually has.
//
// # The contract
//
// Startup refuses only when NO request (or query) can succeed. A network reachable only through a
// fallback or retry branch does NOT become a startup requirement.
//
// # HTTP/3, which is why the requirement is disjunctive
//
// `http3FallbackTransport` reaches the H2 fallback unconditionally for non-`https` and websocket
// requests (http3_transport.go:132-134), when the authority verdict is armed (:140-142), on a cached
// H3 failure (:148-151), and for every replayable `https` request once the H3 leg fails (:235-243) or
// the 300ms delay elapses (:216-219). MEASURED, a TCP-only detour answers a replayable `https` GET
// with 200 from an H2 origin, and a UDP-only detour answers it with 200 from an H3 origin. The single
// class with no fallback is a NON-replayable body on a fresh authority (:152-160) - and that failure
// itself arms the verdict (:158), so it is transient rather than permanent.
//
// Neither transport is individually required; carrying NEITHER is the only provably fatal state.
//
// # DNS `udp`, which is why TCP is conditional
//
// MEASURED against real servers: an untruncated query over a UDP-only detour returns with zero TCP
// dials, and a truncated one fails with exactly one. `exchangeTCP` has two call sites and both are
// inside the `response.Truncated` branch (udp.go:116, :127). UDP is required for any query; TCP is a
// retry capability.
func TestConsumerDemandContractIsProvableOnly(t *testing.T) {
	t.Parallel()

	t.Run("http client", func(t *testing.T) {
		t.Parallel()
		accepted := map[string]struct{ client, detour string }{
			// H3 alone: UDP for any request, so a UDP detour serves it.
			"forced H3 over UDP": {
				client: `{"tag": "hc", "version": 3, "disable_version_fallback": true, "detour": "d"}`,
				detour: svTrueFinalUDPOnly,
			},
			// H3 with its fallback: EITHER transport serves, so both single-network detours work.
			"H3 with fallback over TCP": {
				client: `{"tag": "hc", "version": 3, "detour": "d"}`,
				detour: svTrueFinalTCPOnly,
			},
			"H3 with fallback over UDP": {
				client: `{"tag": "hc", "version": 3, "detour": "d"}`,
				detour: svTrueFinalUDPOnly,
			},
			"H3 with fallback over both": {
				client: `{"tag": "hc", "version": 3, "detour": "d"}`,
				detour: svTrueFinalDual,
			},
			"HTTP/1-2 over TCP": {
				client: `{"tag": "hc", "version": 2, "detour": "d"}`,
				detour: svTrueFinalTCPOnly,
			},
		}
		for name, testCase := range accepted {
			name, testCase := name, testCase
			t.Run("accepted: "+name, func(t *testing.T) {
				t.Parallel()
				instance, err := newBoxFromConfig(t, svHTTPClientConfig(testCase.client, testCase.detour))
				require.NoError(t, err)
				t.Cleanup(func() { _ = instance.Close() })
				require.NoError(t, instance.Start())
			})
		}

		refused := map[string]struct{ client, detour string }{
			// H3 alone over TCP: there is no H2 transport at all, so nothing can be sent.
			"forced H3 over TCP": {
				client: `{"tag": "hc", "version": 3, "disable_version_fallback": true, "detour": "d"}`,
				detour: svTrueFinalTCPOnly,
			},
			// H1/H2 over UDP: neither transport has a UDP path.
			"HTTP/1-2 over UDP": {
				client: `{"tag": "hc", "version": 2, "detour": "d"}`,
				detour: svTrueFinalUDPOnly,
			},
		}
		for name, testCase := range refused {
			name, testCase := name, testCase
			t.Run("refused: "+name, func(t *testing.T) {
				t.Parallel()
				instance, err := newBoxFromConfig(t, svHTTPClientConfig(testCase.client, testCase.detour))
				require.NoError(t, err)
				t.Cleanup(func() { _ = instance.Close() })
				require.Error(t, instance.Start())
			})
		}
	})

	t.Run("dns transport", func(t *testing.T) {
		t.Parallel()
		accepted := map[string]struct{ transportType, detour string }{
			// An ordinary response completes over UDP alone; only the truncation retry is lost.
			"udp over UDP":   {transportType: "udp", detour: svTrueFinalUDPOnly},
			"udp over both":  {transportType: "udp", detour: svTrueFinalDual},
			"tcp over TCP":   {transportType: "tcp", detour: svTrueFinalTCPOnly},
			"tls over TCP":   {transportType: "tls", detour: svTrueFinalTCPOnly},
			"https over TCP": {transportType: "https", detour: svTrueFinalTCPOnly},
			"quic over UDP":  {transportType: "quic", detour: svTrueFinalUDPOnly},
			"h3 over UDP":    {transportType: "h3", detour: svTrueFinalUDPOnly},
		}
		for name, testCase := range accepted {
			name, testCase := name, testCase
			t.Run("accepted: "+name, func(t *testing.T) {
				t.Parallel()
				instance, err := newBoxFromConfig(t, svDNSTransportConfig(testCase.transportType, testCase.detour))
				require.NoError(t, err)
				t.Cleanup(func() { _ = instance.Close() })
				require.NoError(t, instance.Start())
			})
		}

		refused := map[string]struct{ transportType, detour string }{
			"udp over TCP":   {transportType: "udp", detour: svTrueFinalTCPOnly},
			"tcp over UDP":   {transportType: "tcp", detour: svTrueFinalUDPOnly},
			"tls over UDP":   {transportType: "tls", detour: svTrueFinalUDPOnly},
			"https over UDP": {transportType: "https", detour: svTrueFinalUDPOnly},
			"quic over TCP":  {transportType: "quic", detour: svTrueFinalTCPOnly},
			"h3 over TCP":    {transportType: "h3", detour: svTrueFinalTCPOnly},
		}
		for name, testCase := range refused {
			name, testCase := name, testCase
			t.Run("refused: "+name, func(t *testing.T) {
				t.Parallel()
				instance, err := newBoxFromConfig(t, svDNSTransportConfig(testCase.transportType, testCase.detour))
				require.NoError(t, err)
				t.Cleanup(func() { _ = instance.Close() })
				require.Error(t, instance.Start())
			})
		}
	})
}

// TestTheDefaultDomainResolverIsAGraphEdge pins the edge the cross-kind graph was missing.
//
// `route.default_domain_resolver` is what an object with NO resolver of its own actually resolves
// through, so it closes the same runtime loop that an explicit `domain_resolver` closes. MEASURED
// before the edge existed: the default-resolver twin of a refused cycle STARTED, so the graph
// described a different graph from the one the dial path runs.
func TestTheDefaultDomainResolverIsAGraphEdge(t *testing.T) {
	t.Parallel()

	// The control: the cycle closed by an EXPLICIT resolver, which was already refused.
	explicit, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "dns": {"servers": [{"tag": "remote", "type": "udp", "server": "8.8.8.8", "detour": "proxy"}]},
  "outbounds": [{"type": "socks", "tag": "proxy", "server": "127.0.0.1", "server_port": 1080,
                 "version": "5", "domain_resolver": "remote"}],
  "route": {"final": "proxy"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = explicit.Close() })
	require.Error(t, explicit.Start(), "the explicit-resolver cycle is the known-refused shape")

	// The same cycle, closed only through the NETWORK DEFAULT. It must be refused too.
	viaDefault, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "dns": {"servers": [{"tag": "remote", "type": "udp", "server": "8.8.8.8", "detour": "proxy"}]},
  "outbounds": [{"type": "socks", "tag": "proxy", "server": "127.0.0.1", "server_port": 1080,
                 "version": "5"}],
  "route": {"default_domain_resolver": {"server": "remote"}, "final": "proxy"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = viaDefault.Close() })
	require.Error(t, viaDefault.Start(),
		"where the resolver is NAMED does not change whether the loop closes at run time")

	// The acyclic control: the same objects with the detour removed must start.
	acyclic, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "dns": {"servers": [{"tag": "remote", "type": "udp", "server": "8.8.8.8"}]},
  "outbounds": [{"type": "socks", "tag": "proxy", "server": "127.0.0.1", "server_port": 1080,
                 "version": "5", "domain_resolver": "remote"},
                {"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = acyclic.Close() })
	require.NoError(t, acyclic.Start(), "no cycle, so nothing may refuse it")
}
