package wireguard

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/route"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// `reserved` and AmneziaWG are mutually exclusive at construction: the bytes
// `reserved` rewrites are header-protection bytes on an AWG packet, and the
// client bind stops rewriting them as soon as AWG is configured. Refusing the
// pair is what keeps a valid configuration from silently meaning "only one of
// these two applies".
func TestAWGReservedIsRejected(t *testing.T) {
	endpoint, err := NewEndpoint(
		context.Background(),
		nil,
		log.NewNOPFactory().Logger(),
		"wg-awg-reserved",
		option.WireGuardEndpointOptions{
			PrivateKey:       "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
			AmneziaWGOptions: option.AmneziaWGOptions{Jc: 4},
			Peers: []option.WireGuardPeer{{
				Address:    "127.0.0.1",
				Port:       51821,
				PublicKey:  "yMfGxcTDwsHAv769vLu6ubi3trW0s7KxsK+urayrqqk=",
				AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
				Reserved:   []uint8{1, 2, 3},
			}},
		},
	)
	require.Error(t, err, "AWG with reserved bytes must be refused at construction")
	require.ErrorContains(t, err, "reserved")
	require.Nil(t, endpoint)
}

// The negative control: an AWG configuration WITHOUT reserved bytes is not
// refused by that rule. Under with_awg the full construction runs (so it may
// still fail on something unrelated to this rule); without the tag it fails on
// inclusion. Either way the error must not be the reserved one.
func TestAWGWithoutReservedIsNotRejectedByTheReservedRule(t *testing.T) {
	networkManager, err := route.NewNetworkManager(context.Background(), logger.NOP(), option.RouteOptions{}, option.DNSOptions{})
	require.NoError(t, err)
	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), networkManager)
	endpoint, endpointErr := NewEndpoint(
		ctx,
		nil,
		log.NewNOPFactory().Logger(),
		"wg-awg-plain",
		option.WireGuardEndpointOptions{
			PrivateKey:       "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
			AmneziaWGOptions: option.AmneziaWGOptions{Jc: 4},
			Peers: []option.WireGuardPeer{{
				Address:    "127.0.0.1",
				Port:       51821,
				PublicKey:  "yMfGxcTDwsHAv769vLu6ubi3trW0s7KxsK+urayrqqk=",
				AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
			}},
		},
	)
	if endpointErr != nil {
		require.NotContains(t, endpointErr.Error(), "reserved",
			"the reserved rule must not reject a configuration that has no reserved bytes")
	} else {
		require.NotNil(t, endpoint)
	}
}
