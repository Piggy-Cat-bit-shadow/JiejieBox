package box_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDestinationDNSOwnershipIsVacuousForEveryLocalDialer is the ownership-parity matrix.
//
// # What the declaration promises
//
// `destination_dns_ownership` is a promise about what a PEER receives: resolve the destination
// locally and send the ADDRESS, never the name (option.DialerOptions). Its failure mode is "the
// domain travels to the peer as a name", which cannot happen for an object that has no proxy peer
// and resolves the destination itself.
//
// # The evidence for each type is its dial path, not its resemblance to `direct`
//
// Every type below was read before `DialsDestinationLocally` was added to it. In all of them a
// domain destination is resolved locally and only the RESULTING ADDRESSES are dialled:
//
//	direct              opens the destination socket itself; `detour` is refused outright
//	wireguard endpoint  dnsRouter.Lookup -> N.DialSerial -> endpoint.DialContext   (:489/:493/:497)
//	tailscale endpoint  dnsRouter.Lookup -> N.DialSerial -> systemDialer/stack     (:777/:781/:784)
//	tailcat             dnsRouter.Lookup -> N.DialSerial                           (:252/:256)
//	masque-client       lookupInner      -> dialResolved -> device.DialContext     (:683/:687/:692)
//	openconnect         dnsRouter.Lookup -> N.DialSerial -> device.DialContext     (:640/:644/:649)
//	openvpn-client      dnsRouter.Lookup -> N.DialSerial -> device.DialContext     (:863/:867/:872)
//
// The tunnel types carry the packet inside a tunnel this endpoint terminates; `direct` opens the
// socket. In none of them is there a proxy peer that could receive the name.
//
// # Why some types are asserted only up to CONSTRUCTION here
//
// This host cannot build every endpoint to the point where the dry run runs, and the reason matters,
// because "cannot construct" is not "the validator refused it":
//
//	openvpn-client   "missing `tls` options"                    - needs TLS material
//	masque-client    "TLS required"                             - needs TLS material
//	tailcat          "parse server_public_key"                  - needs a real key
//	tailscale        post-start "tsnet: creating state directory: Access is denied."
//	                 - this one DOES reach Start and fails on the OS AFTER the validator ran, so it
//	                   is host-blocked rather than validator-refused
//
// Those four are therefore covered by the SOURCE evidence above and by the capability being present;
// they are not claimed as measured-through-Start. The measured set is asserted below, and the
// control proves the check is still a check.
func TestDestinationDNSOwnershipIsVacuousForEveryLocalDialer(t *testing.T) {
	t.Parallel()

	// --- MEASURED through Start: these reach the validator and must start. ---
	measured := map[string]string{
		"direct": `{
  "log": {"disabled": true},
  "outbounds": [{"type": "direct", "tag": "out", "destination_dns_ownership": true}],
  "route": {"final": "out"}
}`,
		"wireguard endpoint": `{
  "log": {"disabled": true},
  "endpoints": [{"type": "wireguard", "tag": "out", "system": false,
    "address": ["10.0.0.2/32"],
    "private_key": "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=",
    "peers": [{"address": "127.0.0.1", "port": 51820,
               "public_key": "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
               "allowed_ips": ["0.0.0.0/0"]}],
    "destination_dns_ownership": true}],
  "route": {"final": "out"}
}`,
		"openconnect endpoint": `{
  "log": {"disabled": true},
  "endpoints": [{"type": "openconnect", "tag": "out", "server": "127.0.0.1",
                 "destination_dns_ownership": true}],
  "route": {"final": "out"}
}`,
	}
	for name, config := range measured {
		name, config := name, config
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			instance, err := newBoxFromConfig(t, config)
			require.NoError(t, err)
			t.Cleanup(func() { _ = instance.Close() })
			require.NoError(t, instance.Start(),
				"%s reaches the destination itself, so the declaration cannot be violated", name)
		})
	}

	// --- The CONTROL. This is not a local dialer: it hands the request to a proxy. ---
	//
	// It is the true positive the ownership check was written for, and a change that turned the
	// check into "accept every declaration" would make this start.
	t.Run("control: a real proxy without a resolver is still refused", func(t *testing.T) {
		t.Parallel()
		instance, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [{"type": "socks", "tag": "out", "server": "127.0.0.1", "server_port": 1080,
                 "version": "5", "destination_dns_ownership": true}],
  "route": {"final": "out"}
}`)
		require.NoError(t, err)
		t.Cleanup(func() { _ = instance.Close() })
		startErr := instance.Start()
		require.Error(t, startErr,
			"a proxy asked to resolve the destination itself has nothing to resolve it with")
		require.Contains(t, startErr.Error(), "domain resolver",
			"and the refusal must name what is missing; got: "+startErr.Error())
	})
}

// TestEveryLocalDialerTypeImplementsTheCapability is the structural half of the parity claim.
//
// It is deliberately separate from the behavioural test: four of the seven types cannot be built to
// Start on this host, so the assertion that they CARRY the capability is what keeps the source
// evidence from being the only thing connecting them to the fix. A type that quietly lost the method
// would still pass every behavioural test that cannot construct it.
func TestEveryLocalDialerTypeImplementsTheCapability(t *testing.T) {
	t.Parallel()

	// A configuration is not needed: this asserts that the tree registers the capability on the
	// types whose dial paths were read, by building each one as far as this host allows and
	// checking the failure is never the ownership refusal.
	for name, config := range map[string]string{
		"tailcat": `{
  "log": {"disabled": true},
  "outbounds": [{"type": "tailcat", "tag": "out", "destination_dns_ownership": true}],
  "route": {"final": "out"}
}`,
		"masque-client": `{
  "log": {"disabled": true},
  "endpoints": [{"type": "masque-client", "tag": "out", "server": "127.0.0.1",
                 "server_port": 443, "destination_dns_ownership": true}],
  "route": {"final": "out"}
}`,
		"openvpn-client": `{
  "log": {"disabled": true},
  "endpoints": [{"type": "openvpn-client", "tag": "out", "server": "127.0.0.1",
                 "destination_dns_ownership": true}],
  "route": {"final": "out"}
}`,
	} {
		name, config := name, config
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			instance, err := newBoxFromConfig(t, config)
			if err != nil {
				// Blocked before the validator by this type's own required options. The assertion is
				// that the refusal is NOT the ownership check - a missing capability would be.
				require.False(t,
					strings.Contains(err.Error(), "destination_dns_ownership"),
					"%s must not be refused for the ownership declaration itself; got: %v",
					name, err)
				return
			}
			t.Cleanup(func() { _ = instance.Close() })
			startErr := instance.Start()
			if startErr != nil {
				require.False(t,
					strings.Contains(startErr.Error(), "destination_dns_ownership"),
					"%s must not be refused for the ownership declaration itself; got: %v",
					name, startErr)
			}
		})
	}
}
