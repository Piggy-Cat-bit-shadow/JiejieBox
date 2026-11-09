package group

// What the PRODUCTION key actually names.
//
// `common/urltest` proves that every field of MeasurementKey separates two questions. This test is
// the other half: it reads the key the URLTest batch really builds, so a change that makes one of
// the empty dimensions vary - a per-call method, a TLS policy, a detour path, a per-call instance
// name - has to come here and be accounted for instead of silently collapsing two questions into
// one.

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

type keyTestOutbound struct {
	adapter.Outbound
	tag string
}

func (o *keyTestOutbound) Type() string      { return "key-test" }
func (o *keyTestOutbound) Tag() string       { return o.tag }
func (o *keyTestOutbound) Network() []string { return []string{N.NetworkTCP} }

func (o *keyTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}

func (o *keyTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// epochOnlyManager is a NetworkManager with one working method: the epoch is all the key reads.
type epochOnlyManager struct {
	adapter.NetworkManager
	epoch uint64
}

func (m *epochOnlyManager) NetworkEnvironment() uint64 { return m.epoch }

func TestTheProductionMeasurementKeyNamesEveryFactThatCanVary(t *testing.T) {
	expected, err := urltest.ParseExpectedStatus("204")
	require.NoError(t, err)
	// The purpose comes from the round's evidence layer, so the round has to be named here.
	// A urlTestBatch with the zero mode is a DISPLAY round by design; a test that wants the
	// health key must say so, exactly as every production caller does.
	batch := &urlTestBatch{expected: expected, mode: TestHistoryHealth}
	outbound := &keyTestOutbound{tag: "node-a"}
	link := "https://probe.example/generate_204"

	key := batch.measurementKey(context.Background(), link, outbound)
	require.NotNil(t, key)

	// The facts the batch knows and must name.
	require.Equal(t, "node-a", key.Node)
	require.Equal(t, link, key.Target)
	require.Equal(t, expected.Key(), key.ExpectedStatus)
	require.Equal(t, urltest.PurposeHealth, key.Purpose,
		"a health round must never share evidence with a display or diagnostic probe")
	require.NotEmpty(t, key.NodeIdentity,
		"the tag is a name, not an identity: a provider refresh replaces the OBJECT under the same tag")

	// The purpose follows the round's evidence layer, so a display round cannot claim health.
	displayBatch := &urlTestBatch{expected: expected, mode: TestHistoryDisplayOnly}
	displayKey := displayBatch.measurementKey(context.Background(), link, outbound)
	require.Equal(t, urltest.PurposeDisplay, displayKey.Purpose)
	require.NotEqual(t, key.Purpose, displayKey.Purpose,
		"the two layers must not collapse into one measurement question")
	require.False(t, key.SharesEvidenceWith(*displayKey),
		"a manual/display measurement is never shared as health evidence")
	require.NotEqual(t, key.NodeIdentity, (&urlTestBatch{expected: expected, mode: TestHistoryHealth}).measurementKey(
		context.Background(), link, &keyTestOutbound{tag: "node-a"}).NodeIdentity,
		"two objects with the same tag are two identities")

	// Without a network manager the epoch is zero, which means "no cross-generation sharing"
	// rather than "share with everything".
	require.Zero(t, key.NetworkEpoch)

	// The facts that cannot vary today, and why each is empty rather than invented:
	//
	//   - Instance: the registry itself is the scope. A Box's share is created per Box, so two
	//     Boxes cannot reach one registry; naming an instance here would only make two callers
	//     that ARE equivalent spell the key differently and lose the share.
	//   - Path: the value passed to Measure IS the leaf outbound, and its tag already identifies
	//     the route it dials. A group measuring a nested member recurses to the leaf first.
	//   - Method: Measure performs HEAD for every caller; MeasureOptions has no method knob.
	//   - TLSFingerprint: the measurement transport's TLS policy is fixed in newMeasurementTransport
	//     and no caller can choose another.
	//
	// If any of those becomes callable, the corresponding field must be filled here in the same
	// change - an empty field would then mean "two different questions share one answer".
	require.Empty(t, key.Instance)
	require.Empty(t, key.Path)
	require.Empty(t, key.Method)
	require.Empty(t, key.TLSFingerprint)

	// The epoch is the one dimension that comes from the Box, and it is carried.
	epochCtx := service.ContextWith[adapter.NetworkManager](context.Background(), &epochOnlyManager{epoch: 12})
	epochKey := batch.measurementKey(epochCtx, link, outbound)
	require.EqualValues(t, 12, epochKey.NetworkEpoch)
	require.False(t, key.SharesEvidenceWith(*epochKey),
		"an answer measured on the previous network must not answer a question about this one")
}
