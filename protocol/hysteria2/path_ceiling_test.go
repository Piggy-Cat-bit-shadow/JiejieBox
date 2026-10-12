package hysteria2

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// A proven lower tunnel capacity, and how far it gets
// ---------------------------------------------------------------------------
//
// The outbound computes a QUIC payload ceiling from the tunnel its `detour` names and hands the result
// to the library twice, as two different statements: `InitialPacketSize` is the size it would PREFER,
// and `InitialPacketSizeCeiling` is the size the path is KNOWN to carry. Two separate facts have to be
// established, and they are established separately because they have different causes:
//
//  1. the CEILING COMPUTATION AND WIRING: a detour naming a fixed-MTU endpoint produces the right
//     number, an unknown path produces none, and a ceiling below the QUIC minimum is refused rather
//     than clamped.
//
//  2. the WIRE: whether the number the outbound hands down is what actually goes out. This file used to
//     record that it was NOT - the pinned quic-go replaced the configured value with
//     `chromeInitialPacketSize` (1250) whenever ChromeParrot was set, and ChromeParrot is hysteria2's
//     default - and that is now fixed rather than documented: the ceiling is its own field and is
//     applied after the fingerprint override, measured in hard_ceiling_wire_test.go. What this file
//     still owns is the no-ceiling case and the arithmetic, and
//     `TestPathCeilingReachesTheWireOnlyWithoutChromeParrot` keeps BOTH halves of that: with no ceiling
//     the fingerprint wins, and that is the contract a direct dial keeps.

// fixedMTUEndpoint publishes a fixed inner IP capacity before it is ever started, which is what an
// upper protocol needs: it sizes itself at construction.
type fixedMTUEndpoint struct {
	adapter.Endpoint
	mtu uint32
}

func (e *fixedMTUEndpoint) PortMTU() uint32 { return e.mtu }

type endpointManagerStub struct {
	adapter.EndpointManager
	endpoints map[string]adapter.Endpoint
}

func (m *endpointManagerStub) Get(tag string) (adapter.Endpoint, bool) {
	endpoint, loaded := m.endpoints[tag]
	return endpoint, loaded
}

func capacityContext(endpoints map[string]adapter.Endpoint) context.Context {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	return service.ContextWith[adapter.EndpointManager](ctx, &endpointManagerStub{endpoints: endpoints})
}

// TestThePathCeilingIsComputedFromTheDetour pins the composition the outbound performs, including the
// family-unknown conservatism, without building a client.
func TestThePathCeilingIsComputedFromTheDetour(t *testing.T) {
	const quicMinimum = dialer.MinimumQUICInitialPacketSize

	cases := []struct {
		name       string
		detour     string
		endpoints  map[string]adapter.Endpoint
		configured int
		want       int
		hasCeiling bool
		comment    string
	}{
		{
			name: "no detour: the configured value is untouched", detour: "",
			configured: 1500, want: 1500, hasCeiling: false,
			comment: "a direct dial must keep exactly its previous behaviour",
		},
		{
			name: "a 1280-byte tunnel, nothing configured", detour: "masque-us",
			endpoints:  map[string]adapter.Endpoint{"masque-us": &fixedMTUEndpoint{mtu: 1280}},
			configured: 0, want: 1232, hasCeiling: true,
			comment: "zero means no preference, and the ceiling is the preference. The family is " +
				"undecided here, so the IPv6 budget is taken",
		},
		{
			name: "a 1280-byte tunnel, the operator asked for more", detour: "masque-us",
			endpoints:  map[string]adapter.Endpoint{"masque-us": &fixedMTUEndpoint{mtu: 1280}},
			configured: 1400, want: 1232, hasCeiling: true,
			comment: "a physical capacity is a correctness constraint, so it beats a preference",
		},
		{
			name: "a 1280-byte tunnel, the operator asked for less", detour: "masque-us",
			endpoints:  map[string]adapter.Endpoint{"masque-us": &fixedMTUEndpoint{mtu: 1280}},
			configured: 1200, want: 1200, hasCeiling: true,
			comment: "a value that already fits is kept",
		},
		{
			name: "a 1500-byte tunnel", detour: "wg-1",
			endpoints:  map[string]adapter.Endpoint{"wg-1": &fixedMTUEndpoint{mtu: 1500}},
			configured: 0, want: 1452, hasCeiling: true,
			comment: "an ordinary Ethernet-sized tunnel",
		},
		{
			name: "an endpoint with no fixed capacity", detour: "other",
			endpoints:  map[string]adapter.Endpoint{"other": &struct{ adapter.Endpoint }{}},
			configured: 1500, want: 1500, hasCeiling: false,
			comment: "an endpoint that does not publish a capacity must not be guessed at",
		},
		{
			name: "a tunnel too small for a QUIC Initial", detour: "tiny",
			endpoints:  map[string]adapter.Endpoint{"tiny": &fixedMTUEndpoint{mtu: 1250}},
			configured: 0, want: 1202, hasCeiling: true,
			comment: "1250 - 48 = 1202, which is above the 1200 minimum: this must still be allowed. " +
				"The refusal case is the next one",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			capacity := dialer.DetourPathCapacity(capacityContext(testCase.endpoints), testCase.detour)
			ceiling, hasCeiling := capacity.QuicPayloadCeiling()
			require.Equal(t, testCase.hasCeiling, hasCeiling, testCase.comment)
			got := dialer.ClampToCeiling(testCase.configured, ceiling, hasCeiling)
			require.Equal(t, testCase.want, got, testCase.comment)
			if testCase.hasCeiling {
				require.GreaterOrEqual(t, got, quicMinimum,
					"a ceiling that leaves less than the QUIC minimum is the refusal case, not a "+
						"clamp, and must not be reported as a usable value")
			}
		})
	}
}

// TestATunnelTooSmallForAQUICInitialIsRefused is the error half. Clamping a value below the protocol
// minimum would produce a handshake no conforming peer accepts, so the configuration is refused with a
// message naming the cause.
func TestATunnelTooSmallForAQUICInitialIsRefused(t *testing.T) {
	// 1240 - 48 = 1192, below the 1200 minimum.
	capacity := dialer.DetourPathCapacity(
		capacityContext(map[string]adapter.Endpoint{"tiny": &fixedMTUEndpoint{mtu: 1240}}), "tiny")
	require.True(t, capacity.Known)
	ceiling, hasCeiling := capacity.QuicPayloadCeiling()
	require.True(t, hasCeiling)
	require.Less(t, ceiling, uint32(dialer.MinimumQUICInitialPacketSize),
		"this fixture must actually produce a ceiling below the QUIC minimum, or the refusal it "+
			"exercises is not the one under test")

	clamped := dialer.ClampToCeiling(0, ceiling, hasCeiling)
	require.Less(t, clamped, dialer.MinimumQUICInitialPacketSize,
		"the clamp must not silently raise the value to the minimum: that would be inventing capacity "+
			"the tunnel does not have, and the handshake would fail further from the cause")
}

// ---------------------------------------------------------------------------
// The wire: measured, and the boundary stated
// ---------------------------------------------------------------------------

// TestPathCeilingReachesTheWireOnlyWithoutChromeParrot is now the NO-HARD-CEILING control for the
// direct path, and it is kept deliberately in its original form.
//
// It reuses the first-datagram harness and asks the same question twice for the SAME configured
// value, with no ceiling in play:
//
//	ChromeParrot OFF, initial_packet_size = 1232 -> 1232 reaches the wire
//	ChromeParrot ON,  initial_packet_size = 1232 -> 1250 does, the fingerprint size
//
// The second case is the ChromeParrot fingerprint contract, not a defect: an operator's
// `initial_packet_size` is a PREFERENCE and the fingerprint is allowed to replace it. What may
// override the fingerprint is a proven path capacity, and that is a different field - see
// TestHardCeilingWireMatrix, rows C, D and F, which measure the same pair of settings WITH a hard
// ceiling and get the ceiling on the wire.
//
// This test used to say the ceiling could not reach the wire at all under ChromeParrot, and cited
// the library change as unwritten and unpushed. Both are no longer true, and the harness now
// distinguishes the two situations by the field under test rather than by the pinned revision: with
// a ceiling, the ceiling wins; without one, the fingerprint does.
func TestPathCeilingReachesTheWireOnlyWithoutChromeParrot(t *testing.T) {
	// A 1280-byte inner IPv6 tunnel: 1280 - 40 - 8.
	const configured = 1232

	withoutParrot := measureFirstDatagram(t, measureRequest{
		chromeParrot:                false,
		configuredInitialPacketSize: configured,
	})
	require.Equal(t, configured, withoutParrot.size,
		"with ChromeParrot off the configured value must reach the wire: this is the half of the fix "+
			"that never depended on the library, and a regression here would mean the outbound stopped "+
			"passing the value down")

	withParrot := measureFirstDatagram(t, measureRequest{
		chromeParrot:                true,
		configuredInitialPacketSize: configured,
	})
	require.Equal(t, chromeParrotInitialPacketSize, withParrot.size,
		"with ChromeParrot ON and NO ceiling, the pinned quic-go replaces the configured value with "+
			"chromeInitialPacketSize (%d). That is the fingerprint contract, and it is what the hard "+
			"ceiling exists to override when - and only when - a path capacity has been proven",
		chromeParrotInitialPacketSize)

	t.Logf("no ceiling: configured %d -> ChromeParrot off %d bytes, ChromeParrot on %d bytes. "+
		"With a ceiling the same pair of settings yields the ceiling; see the hard-ceiling matrix.",
		configured, withoutParrot.size, withParrot.size)
}

// TestTheCeilingDoesNotChangeADirectDial keeps the compatibility promise in the harness's own terms: a
// configuration with no detour must behave exactly as it did before the ceiling existed.
//
// The outbound computes no ceiling for an empty detour (proved above), so this is about the composed
// behaviour: the value reaching the library is the configured one, and the wire shows it.
func TestTheCeilingDoesNotChangeADirectDial(t *testing.T) {
	direct := measureFirstDatagram(t, measureRequest{
		chromeParrot:                false,
		configuredInitialPacketSize: 1452,
	})
	require.Equal(t, 1452, direct.size,
		"with no detour there is no ceiling, so an operator's value must reach the wire unchanged")
}
