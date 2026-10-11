package box_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDestinationDNSOwnershipIsVacuousForALocalDialer is the FD-OWNERSHIP false deny.
//
// # What the declaration promises, and why `direct` cannot break it
//
// `destination_dns_ownership` is documented (option.DialerOptions) as a statement about a DOWNSTREAM
// PROXY: with it enabled, a domain destination is resolved locally and the outbound sends the
// resulting ADDRESS to the peer, never the domain. Its failure mode is therefore "the domain travels
// to the peer as a name".
//
// `direct` has no peer and writes no request - it opens an operating-system socket to the
// destination (`protocol/direct/outbound.go`, `DirectOutbound: true`), and it refuses `detour`
// outright, so no other hop can take the request either. There is nothing to promise and nothing to
// violate, yet the check refused the configuration with "the destination domain would be sent to the
// peer", which describes an event that cannot happen.
//
// # Why the positive control is the important half
//
// "Accept everything" would satisfy the false-deny half. The control below is the shape the check
// exists for - a real proxy whose domain resolution has no authority - and it must STILL be refused.
func TestDestinationDNSOwnershipIsVacuousForALocalDialer(t *testing.T) {
	t.Parallel()

	// Accepted: `direct` reaches the destination itself, so the declaration is satisfied by
	// construction.
	localDialer, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "direct", "tag": "out", "destination_dns_ownership": true}
  ],
  "route": {"final": "out"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = localDialer.Close() })
	require.NoError(t, localDialer.Start(),
		"`direct` has no peer to send a domain to, so the declaration cannot be violated and must "+
			"not refuse the configuration")

	// Refused: a real proxy that HONOURS the declaration still needs an authority to resolve with.
	// This is the true positive the check was written for, and a fix that widened the check into
	// "accept every declaration" would turn it green.
	proxyWithoutResolver, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "out", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "destination_dns_ownership": true}
  ],
  "route": {"final": "out"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = proxyWithoutResolver.Close() })
	startErr := proxyWithoutResolver.Start()
	require.Error(t, startErr,
		"a proxy asked to resolve the destination itself has nothing to resolve it with")
	require.Contains(t, startErr.Error(), "domain resolver",
		"and the refusal must name what is missing; got: "+startErr.Error())

	// Accepted: the same proxy with an authority configured starts.
	proxyWithResolver, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "dns": {"servers": [{"tag": "dns1", "type": "udp", "server": "8.8.8.8"}]},
  "outbounds": [
    {"type": "socks", "tag": "out", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "destination_dns_ownership": true, "domain_resolver": "dns1"}
  ],
  "route": {"final": "out"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = proxyWithResolver.Close() })
	require.NoError(t, proxyWithResolver.Start(),
		"the declaration is honoured and the resolver exists, so this must start")
}

// TestALocalDialerWithoutTheDeclarationStillStarts is the untouched control.
//
// The new branch only runs for a tag whose configuration DECLARED ownership, so an ordinary `direct`
// outbound must be unaffected. It is asserted separately because a branch placed one level too high
// would silently change every configuration in the tree, and the tests that would notice are not the
// ones in this file.
func TestALocalDialerWithoutTheDeclarationStillStarts(t *testing.T) {
	t.Parallel()

	instance, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "direct", "tag": "out"}
  ],
  "route": {"final": "out"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = instance.Close() })
	require.NoError(t, instance.Start())
}
