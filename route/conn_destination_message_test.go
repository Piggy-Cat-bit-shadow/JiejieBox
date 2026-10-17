package route

import (
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The destination must be IN the message a failed connection reports, for every arm that reports one.
//
// # The production evidence
//
// Sixteen unconnected packet flows in one log window failed with:
//
//	connection: listen packet connection using  using outbound/direct[direct]:
//	  local network has no public ipv6 path: ...
//
// Two blanks. The arm built only `dialerString`, so `E.Cause(err, "listen packet connection using ",
// dialerString)` concatenated a phrase ending in a space with a value starting with one, and the
// destination - the only thing that identifies WHICH flow failed - was never in it. The stream arm and
// the connected packet arm both reported the destination, so this was one arm of three, which is the
// shape a regression test can only catch if it asserts on the shared rendering rather than on one
// call site.
//
// # Why the port is asserted separately from the address
//
// The candidate-list form used to render without a port. On the reported machine the same WeChat host
// was refused on port 80 over IPv6 while port 443 worked over a proxy, so a message carrying the
// address alone cannot distinguish the failing flow from the working one. The port is the field that
// discriminates, which is why it is in every form including the list.
func TestDestinationForErrorNamesTheDestination(t *testing.T) {
	ipv6 := netip.MustParseAddr("2409:8c50:a00:40a3::1a")
	ipv4 := netip.MustParseAddr("112.53.55.92")

	for _, testCase := range []struct {
		name     string
		metadata adapter.InboundContext
		expected string
	}{
		{
			// The reported packet flow: a literal destination, no candidate list.
			name:     "literal IPv6 with its port",
			metadata: adapter.InboundContext{Destination: M.SocksaddrFrom(ipv6, 8000)},
			expected: "[2409:8c50:a00:40a3::1a]:8000",
		},
		{
			name:     "literal IPv4 with its port",
			metadata: adapter.InboundContext{Destination: M.SocksaddrFrom(ipv4, 80)},
			expected: "112.53.55.92:80",
		},
		{
			// A resolved name: the candidates carry the answer, and the port comes from the
			// destination the flow was for.
			name: "candidate list keeps its port",
			metadata: adapter.InboundContext{
				Destination:          M.SocksaddrFrom(ipv6, 443),
				DestinationAddresses: []netip.Addr{ipv6, ipv4},
			},
			expected: "[2409:8c50:a00:40a3::1a,112.53.55.92]:443",
		},
		{
			// A caller with candidates and no port: report what is known and do not invent the rest.
			// ":0" would be a claim about a port the flow never had.
			name: "candidate list without a port does not claim one",
			metadata: adapter.InboundContext{
				DestinationAddresses: []netip.Addr{ipv4},
			},
			expected: "[112.53.55.92]",
		},
		{
			name:     "domain destination is rendered as the name",
			metadata: adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("szminorshort.weixin.qq.com", 80)},
			expected: "szminorshort.weixin.qq.com:80",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			rendered := destinationForError(testCase.metadata)
			require.Equal(t, testCase.expected, rendered)
			require.NotEmpty(t, rendered, "an empty destination is the defect this exists to prevent")
		})
	}
}

// The three arms must render through the one function, so no arm can report a destination-less message
// again. This is the structural half: it reads the source, because the messages are built inline in
// NewConnection / NewPacketConnection and there is no seam to call.
//
// # Why a source assertion is the right shape here, and its limit
//
// The alternative is a fixture that stands up a connection manager, a dialer and a handshake just to
// observe one string. That fixture is what made the previous round's regression test wrong in the same
// direction as the code: elaborate enough to look like production while passing for the wrong reason. A
// source assertion cannot be fooled that way - it fails the moment an arm reintroduces a literal
// prefix - and its limit is that it proves the call shape, not the rendered output.
// TestDestinationForErrorNamesTheDestination proves the rendered output. The two together are the claim;
// neither alone is.
func TestEveryConnectionFailureMessageCarriesTheDestination(t *testing.T) {
	source, err := os.ReadFile("conn.go")
	require.NoError(t, err, "this test reads the file it asserts about, and must fail loudly if it moves")
	text := string(source)

	// Every failure message in this file must route its destination through destinationForError, and
	// must not rebuild one inline. The three prefixes are the three arms.
	for _, prefix := range []string{
		`"open connection to "`,
		`"open packet connection to "`,
		`"listen packet connection to "`,
	} {
		require.Contains(t, text, prefix,
			"arm message %s is missing: an arm that reports a failure without the destination cannot be "+
				"correlated with the inbound line that caused it", prefix)
	}

	// The old, destination-less phrasing. Its reappearance is the exact regression.
	require.NotContains(t, text, `"listen packet connection using "`,
		"`listen packet connection using ` concatenates a phrase ending in a space with a dialer string "+
			"starting with one, and carries no destination: `... using  using outbound/direct[direct]`")

	// Every arm that renders a destination must use the function, not an inline rebuild. Counting is
	// blunt but it is what catches a fourth arm written the old way.
	arms := strings.Count(text, "destinationForError(metadata)")
	require.GreaterOrEqual(t, arms, 3,
		"all three failure arms (stream, connected packet, unconnected packet) must render through "+
			"destinationForError; found %d", arms)
}
