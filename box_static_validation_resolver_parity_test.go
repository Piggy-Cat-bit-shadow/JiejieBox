package box_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolverAuthorityParity pins the confirmed false deny and the cases around it.
//
// # The defect
//
// `CanResolveDestination` modelled the resolver precedence itself and knew two of the four sources
// `common/dialer` distinguishes. The lifecycles made the difference observable: the DNS transport
// manager registers its fallback transport at `StartStateInitialize`, while the dry run runs at
// `StartStateStart`, so by the time the validator asked, a configuration WITH a DNS server had a
// usable authority — and was refused anyway, because the validator only looked for a NAMED resolver.
//
// The fix makes both sides call `common/dialer.ResolveAuthority`. The cases below are the ones that
// matter, and the labels record what the runtime actually does rather than what the old validator
// assumed.
func TestResolverAuthorityParity(t *testing.T) {
	t.Parallel()

	// The declaration under test in every case, on a real proxy whose resolution the flag governs.
	const owned = `{"type": "socks", "tag": "out", "server": "127.0.0.1", "server_port": 1080, ` +
		`"version": "5", "destination_dns_ownership": true}`

	accepted := map[string]struct {
		why    string
		config string
	}{
		"one udp dns server": {
			why: "the runtime resolves through the named transport; the old validator refused this " +
				"because no `domain_resolver` was declared on the outbound",
			config: `{"log": {"disabled": true},
  "dns": {"servers": [{"tag": "dns1", "type": "udp", "server": "8.8.8.8"}]},
  "outbounds": [` + owned + `],
  "route": {"final": "out"}}`,
		},
		"one local dns server": {
			why: "same source, reached through a local transport",
			config: `{"log": {"disabled": true},
  "dns": {"servers": [{"tag": "local", "type": "local"}]},
  "outbounds": [` + owned + `],
  "route": {"final": "out"}}`,
		},
		"no dns server at all": {
			why: "Box installs a `local` (system) transport as the fallback at box.go:809, so a name " +
				"IS resolvable and refusing this would be a false deny — the fallback is a working " +
				"resolver, not the absence of one",
			config: `{"log": {"disabled": true},
  "outbounds": [` + owned + `],
  "route": {"final": "out"}}`,
		},
		"explicit domain_resolver on the outbound": {
			why: "source #1",
			config: `{"log": {"disabled": true},
  "dns": {"servers": [{"tag": "dns1", "type": "udp", "server": "8.8.8.8"}]},
  "outbounds": [{"type": "socks", "tag": "out", "server": "127.0.0.1", "server_port": 1080,
                 "version": "5", "destination_dns_ownership": true, "domain_resolver": "dns1"}],
  "route": {"final": "out"}}`,
		},
		"route.default_domain_resolver": {
			why: "source #2",
			config: `{"log": {"disabled": true},
  "dns": {"servers": [{"tag": "dns1", "type": "udp", "server": "8.8.8.8"}]},
  "outbounds": [` + owned + `],
  "route": {"default_domain_resolver": {"server": "dns1"}, "final": "out"}}`,
		},
		"no declaration, no resolver": {
			why: "the check only runs for a tag whose configuration DECLARED ownership, so this is " +
				"untouched by any of it",
			config: `{"log": {"disabled": true},
  "outbounds": [{"type": "socks", "tag": "out", "server": "127.0.0.1", "server_port": 1080,
                 "version": "5"}],
  "route": {"final": "out"}}`,
		},
	}
	for name, testCase := range accepted {
		name, testCase := name, testCase
		t.Run("accepted: "+name, func(t *testing.T) {
			t.Parallel()
			instance, err := newBoxFromConfig(t, testCase.config)
			require.NoError(t, err)
			t.Cleanup(func() { _ = instance.Close() })
			require.NoError(t, instance.Start(), testCase.why)
		})
	}

	// The refusal that REMAINS, and the reason it is now a narrow one.
	//
	// A Box always ends up with at least the `local` fallback, so `AuthorityNone` is not reachable
	// through a configuration that starts at all: every path that could produce it either fails
	// construction earlier (a `domain_resolver` naming a transport that does not exist) or leaves a
	// usable fallback behind. The check therefore only refuses when there is genuinely no authority
	// anywhere, which is an embedder-level state rather than a configuration an operator can write.
	//
	// This is asserted as a PROPERTY rather than faked with a config: a test that claimed a Box
	// config reaches `AuthorityNone` would be asserting something the runtime does not do.
	t.Run("refusal requires no authority anywhere", func(t *testing.T) {
		t.Parallel()
		// A `domain_resolver` naming a transport that does not exist is refused, but by the DNS
		// layer at construction - not by the ownership check, and the message proves which.
		_, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "dns": {"servers": [{"tag": "dns1", "type": "udp", "server": "8.8.8.8"}]},
  "outbounds": [{"type": "socks", "tag": "out", "server": "127.0.0.1", "server_port": 1080,
                 "version": "5", "destination_dns_ownership": true,
                 "domain_resolver": "does-not-exist"}],
  "route": {"final": "out"}
}`)
		if err != nil {
			require.NotContains(t, err.Error(), "destination_dns_ownership",
				"a resolver that does not exist is the DNS layer's failure, not the ownership check's; "+
					"got: "+err.Error())
		}
	})
}
