package group

// P03 — the selection policy's hysteresis and its slots.
//
// The automatic group does not re-pick the fastest member on every measurement: it keeps the
// member live traffic is on until another member is better by MORE than the tolerance. That
// is what stops a single noisy measurement from moving every stream, and it is the piece a
// deterministic test can pin without a clock, because Select reads stored evidence only.
//
// The cases below are the policy in four sentences:
//   - a member inside the tolerance window does not displace the current one;
//   - a member outside it does;
//   - the two networks keep separate slots;
//   - a member that was never measured cannot be selected at all.

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

const selectPolicyLink = "https://probe.example/generate_204"

// newSelectPolicyGroup builds a group over members without starting a health loop.
func newSelectPolicyGroup(t *testing.T, members ...adapter.Outbound) (*URLTestGroup, *urltest.HistoryStorage) {
	t.Helper()
	return newGroupFixtureInterval(t, selectPolicyLink, 0, members...)
}

// storeSelectDelay records health evidence for one member.
func storeSelectDelay(t *testing.T, storage *urltest.HistoryStorage, tag string, delay uint16) {
	t.Helper()
	scope, err := urltest.NewMeasurementScope(selectPolicyLink, nil)
	require.NoError(t, err)
	storage.StoreHealthHistory(tag, scope, &adapter.URLTestHistory{Time: time.Now(), Delay: delay})
}

// networkOutbound is a member that only claims the given networks.
type networkOutbound struct {
	*probeMarkingOutbound
	networks []string
}

func (o *networkOutbound) Network() []string { return o.networks }

func TestSelectionKeepsTheCurrentMemberInsideTheTolerance(t *testing.T) {
	first := &probeMarkingOutbound{tag: "first"}
	second := &probeMarkingOutbound{tag: "second"}
	group, storage := newSelectPolicyGroup(t, first, second)
	storeSelectDelay(t, storage, "first", 100)
	// 40 ms better is inside the default 50 ms tolerance: a single noisy measurement must
	// not move live traffic.
	storeSelectDelay(t, storage, "second", 60)

	selected, measured := group.Select(N.NetworkTCP)
	require.True(t, measured)
	require.Equal(t, "first", selected.Tag(), "a candidate inside the tolerance must not displace the current member")
}

func TestSelectionSwitchesOnlyBeyondTheTolerance(t *testing.T) {
	first := &probeMarkingOutbound{tag: "first"}
	second := &probeMarkingOutbound{tag: "second"}
	group, storage := newSelectPolicyGroup(t, first, second)
	storeSelectDelay(t, storage, "first", 100)
	// 60 ms better is outside the tolerance: this is degradation, not jitter.
	storeSelectDelay(t, storage, "second", 40)

	selected, measured := group.Select(N.NetworkTCP)
	require.True(t, measured)
	require.Equal(t, "second", selected.Tag(), "a candidate outside the tolerance must win")
}

func TestSelectionKeepsSlotsPerNetwork(t *testing.T) {
	tcpOnly := &networkOutbound{probeMarkingOutbound: &probeMarkingOutbound{tag: "tcp-only"}, networks: []string{N.NetworkTCP}}
	udpOnly := &networkOutbound{probeMarkingOutbound: &probeMarkingOutbound{tag: "udp-only"}, networks: []string{N.NetworkUDP}}
	group, storage := newSelectPolicyGroup(t, tcpOnly, udpOnly)
	storeSelectDelay(t, storage, "tcp-only", 100)
	storeSelectDelay(t, storage, "udp-only", 200)

	tcpSelected, measured := group.Select(N.NetworkTCP)
	require.True(t, measured)
	require.Equal(t, "tcp-only", tcpSelected.Tag())
	udpSelected, measured := group.Select(N.NetworkUDP)
	require.True(t, measured)
	require.Equal(t, "udp-only", udpSelected.Tag(),
		"the two networks keep separate slots; the UDP slot must not be filled by the TCP choice")
}

func TestSelectionNeverPicksAnUnmeasuredMember(t *testing.T) {
	measured := &probeMarkingOutbound{tag: "measured"}
	unmeasured := &probeMarkingOutbound{tag: "unmeasured"}
	group, storage := newSelectPolicyGroup(t, measured, unmeasured)
	storeSelectDelay(t, storage, "measured", 300)

	selected, ok := group.Select(N.NetworkTCP)
	require.True(t, ok)
	require.Equal(t, "measured", selected.Tag(),
		"a member with no evidence for this scope cannot be selected on the strength of being first")
}

func TestSelectionWithoutAnyEvidenceIsUnmeasured(t *testing.T) {
	first := &probeMarkingOutbound{tag: "first"}
	group, _ := newSelectPolicyGroup(t, first)

	selected, measured := group.Select(N.NetworkTCP)
	require.False(t, measured, "nothing was measured, and the answer must say so instead of claiming a measurement")
	require.NotNil(t, selected, "a dial still needs a member to try")
}
