package box_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTheCrossKindGraphReadsPublishedAdapterFacts is the wiring closure.
//
// # The invariant
//
// The graph may only reason about facts published by the same runtime objects whose references it
// walks. Both `adapter/outbound.Adapter` and `adapter/endpoint.Adapter` now publish the two facts the
// graph needs, captured from the real `DialerOptions` at construction:
//
//	DomainResolverReference() string   the object's OWN explicit `domain_resolver`
//	ResolvesDomainServer()   bool      whether its own remote address is a DOMAIN, i.e. whether the
//	                                   runtime builds a ResolveDialer and reads the network default
//
// Those are different facts. An earlier version published the second only on `dns.TransportAdapter` -
// the type the DNS MANAGER holds, not the type the graph walks - and read the endpoint half through a
// separate accessor reached only after the same interface assertion had already failed, so it always
// returned "". Both symptoms are covered below, and the fix is one publication on the adapter.
//
// # Why the two pairs are mandatory
//
// Each refusal needs a near-identical legal twin, or the change is pinned against over-charge but not
// under-charge (or the reverse). The four cases below cross the two facts with the two outcomes.
func TestTheCrossKindGraphReadsPublishedAdapterFacts(t *testing.T) {
	t.Parallel()

	// svCycleConfig builds a DNS transport whose detour closes back into the object under test.
	svCycleConfig := func(outboundOrEndpoint string, isEndpoint bool) string {
		key := "outbounds"
		if isEndpoint {
			key = "endpoints"
		}
		return `{
  "log": {"disabled": true},
  "` + key + `": [` + outboundOrEndpoint + `],
  "dns": {"servers": [{"tag": "remote", "type": "udp", "server": "8.8.8.8", "detour": "ep"}]},
  "route": {"final": "ep"}
}`
	}

	t.Run("endpoint explicit domain_resolver closes a cycle", func(t *testing.T) {
		t.Parallel()
		// REFUSED: the endpoint names `remote`, and `remote` detours back through the endpoint.
		cycle, err := newBoxFromConfig(t, svCycleConfig(`{"type": "openconnect", "tag": "ep",
      "server": "vpn.example.com", "domain_resolver": "remote"}`, true))
		require.NoError(t, err)
		t.Cleanup(func() { _ = cycle.Close() })
		startErr := cycle.Start()
		require.Error(t, startErr,
			"an endpoint's OWN domain_resolver is a real graph edge; missing it accepts a loop that "+
				"closes at run time. This is the case the dead accessor could not see")
		require.Contains(t, startErr.Error(), "circular dependency",
			"and the refusal must be the cross-kind diagnostic; got: "+startErr.Error())
		require.Contains(t, startErr.Error(), "ep",
			"and it must name the endpoint; got: "+startErr.Error())

		// The legal twin: the same endpoint with NO resolver reference of its own, so nothing closes.
		acyclic, err := newBoxFromConfig(t, svCycleConfig(`{"type": "openconnect", "tag": "ep",
      "server": "vpn.example.com"}`, true))
		require.NoError(t, err)
		t.Cleanup(func() { _ = acyclic.Close() })
		require.NoError(t, acyclic.Start(),
			"removing the one closing edge must make it legal")
	})

	t.Run("default resolver on a domain server closes a cycle", func(t *testing.T) {
		t.Parallel()
		// REFUSED: no resolver is NAMED on the object, but its server is a DOMAIN, so the runtime
		// builds a ResolveDialer and reads `route.default_domain_resolver` - which detours back.
		cycle, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "dns": {"servers": [{"tag": "remote", "type": "udp", "server": "8.8.8.8", "detour": "ep"}]},
  "outbounds": [{"type": "socks", "tag": "ep", "server": "proxy.example.com", "server_port": 1080,
                 "version": "5"}],
  "route": {"default_domain_resolver": {"server": "remote"}, "final": "ep"}
}`)
		require.NoError(t, err)
		t.Cleanup(func() { _ = cycle.Close() })
		require.Error(t, cycle.Start(),
			"a domain server means a ResolveDialer exists, so the network default IS read and the "+
				"loop closes")

		// The legal twin: a LITERAL server, so no ResolveDialer is built and the default is never
		// consulted. This is the over-charge guard - the same configuration was refused when the
		// edge was charged unconditionally.
		literal, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "dns": {"servers": [{"tag": "remote", "type": "udp", "server": "8.8.8.8", "detour": "ep"}]},
  "outbounds": [{"type": "socks", "tag": "ep", "server": "127.0.0.1", "server_port": 1080,
                 "version": "5"}],
  "route": {"default_domain_resolver": {"server": "remote"}, "final": "ep"}
}`)
		require.NoError(t, err)
		t.Cleanup(func() { _ = literal.Close() })
		require.NoError(t, literal.Start(),
			"nothing resolves a name here, so no object reads the default and no cycle exists")
	})

	t.Run("the server-domain fact is not the resolver-reference fact", func(t *testing.T) {
		t.Parallel()
		// A DOMAIN server with no named resolver: starts, because the detour target is a DIFFERENT
		// transport than the one that would be read. This pins that the two facts are independent -
		// conflating them (`ResolvesDomainServer == DomainResolverReference != ""`) would refuse it.
		distinct, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "dns": {"servers": [
    {"tag": "remote", "type": "udp", "server": "8.8.8.8"},
    {"tag": "other", "type": "udp", "server": "1.1.1.1", "detour": "ep"}
  ]},
  "outbounds": [{"type": "socks", "tag": "ep", "server": "proxy.example.com", "server_port": 1080,
                 "version": "5"}],
  "route": {"default_domain_resolver": {"server": "remote"}, "final": "ep"}
}`)
		require.NoError(t, err)
		t.Cleanup(func() { _ = distinct.Close() })
		require.NoError(t, distinct.Start(),
			"`ep` reads `remote`, and `remote` does not detour through `ep`, so no loop exists")
	})
}
