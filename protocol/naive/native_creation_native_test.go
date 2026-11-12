//go:build with_naive_outbound

package naive

// End-to-end evidence for the native creation seam (audit Q14/Q34): the production outbound, the
// production inbound on loopback, and the REAL Cronet artifact.
//
// # Why an end-to-end test is required here
//
// The unit tests in native_creation_test.go prove the seam's contract against a fake dialer, but
// they cannot prove that the pinned NATIVE library actually calls the dialer once per physical
// connection - that is a property of the artifact, and the audit's complaint about Q14 was exactly
// that the claim was never tied to the artifact. These tests close that gap on any host that has
// the archive (Windows uses the DLL from the pinned module), and skip with a stated reason when the
// artifact is absent instead of passing on a technicality.
//
// What they prove with the real engine:
//
//   - a socket is really created through the native callback (Created >= 1);
//   - reuse is the ABSENCE of a creation: several CONNECTs over the pooled HTTP/2 session create
//     strictly fewer sockets than flows, which no outer token counter could show;
//   - the process admission gate is consulted at the native creation (the guard's Gate.Requested
//     rises), so the creation budget covers the native creation rather than only the CONNECT;
//   - the counters stay conserved (Attempts == Accounted, InFlight == 0 when idle).

import (
	"context"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// newNaiveCarrierOutboundWithGuard is newNaiveCarrierOutbound plus an optional process guard and
// the skip that the artifact's absence requires.
//
// The skip is explicit: a runner without the native archive cannot observe native creations, and
// silently treating that as success is the failure mode this window exists to remove. The reason
// travels with the skip so an evidence reader can see which host produced which result.
func newNaiveCarrierOutboundWithGuard(t *testing.T, port uint16, certificate string, guard *resourceguard.Guard, quic bool) *Outbound {
	t.Helper()
	ctx := service.ContextWithDefaultRegistry(context.Background())
	ctx = service.ContextWith[adapter.DNSRouter](ctx, &carrierDNSRouter{})
	if guard != nil {
		ctx = resourceguard.NewContext(ctx, guard)
	}
	created, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("native-creation-naive-out"), "native-creation-naive-out",
		option.NaiveOutboundOptions{
			ServerOptions:    option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			Username:         naiveCarrierUser,
			Password:         naiveCarrierPassword,
			QUIC:             quic,
			QUICFallbackToH2: quic,
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{
					Enabled:     true,
					ServerName:  "localhost",
					Certificate: []string{certificate},
				},
			},
		})
	if err != nil {
		if strings.Contains(err.Error(), "library not found") {
			t.Skipf("the native Cronet archive is not installed on this host (%v); "+
				"native creation observation cannot be exercised here", err)
		}
		require.NoError(t, err)
	}
	outbound, isOutbound := created.(*Outbound)
	require.True(t, isOutbound)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().NewLogger("native-creation-naive-out-scope"))
	require.NoError(t, outbound.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	t.Cleanup(func() { _ = outbound.client.Close() })
	return outbound
}

// TestNativeCreationCountsTheRealEnginesSockets runs several flows over the production outbound and
// requires the counters to show real sockets, fewer of them than flows.
func TestNativeCreationCountsTheRealEnginesSockets(t *testing.T) {
	echo := startNaiveCarrierEcho(t)
	router := &naiveCarrierRouter{target: echo}
	port, certificate := startNaiveCarrierInbound(t, router)
	outbound := newNaiveCarrierOutboundWithGuard(t, port, certificate, nil, false)

	before := outbound.NativeCreationStats()
	require.Zero(t, before.Attempts, "an untouched outbound has not created anything")

	const flows = 4
	for index := 0; index < flows; index++ {
		conn := naiveRoundTrip(t, outbound, "native-creation-flow")
		_ = conn.Close()
	}
	require.EqualValues(t, flows, router.streams.Load(), "every flow reached the inbound")

	after := outbound.NativeCreationStats()
	require.GreaterOrEqual(t, after.Created, uint64(1),
		"the engine must have created at least one socket through the native callback")
	require.Less(t, after.Created, uint64(flows),
		"%d sequential CONNECTs over one pooled session must create fewer than %d sockets; "+
			"%d creations means the counter is counting requests, not connections",
		flows, flows, after.Created)
	require.Equal(t, after.Attempts, after.Accounted(),
		"every creation attempt must have exactly one recorded outcome: %+v", after)
	require.Zero(t, after.InFlight, "no creation may still be in flight once the flows ended")
	require.GreaterOrEqual(t, after.MaxInFlight, int64(1))
	require.Equal(t, before.Created+after.Attempts, after.Created+after.Failed+after.Refused+after.TimedOut+after.Canceled,
		"the ledger must balance across the whole run")
}

// TestNativeCreationGoesThroughTheProcessAdmissionGate proves the WIRING, not just the counters: a
// guard registered in the box context must see the native creation.
func TestNativeCreationGoesThroughTheProcessAdmissionGate(t *testing.T) {
	echo := startNaiveCarrierEcho(t)
	router := &naiveCarrierRouter{target: echo}
	port, certificate := startNaiveCarrierInbound(t, router)
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), nil)
	outbound := newNaiveCarrierOutboundWithGuard(t, port, certificate, guard, false)

	conn := naiveRoundTrip(t, outbound, "admitted-native-creation")
	_ = conn.Close()

	gate := guard.Stats().Gate
	require.GreaterOrEqual(t, gate.Requested, uint64(1),
		"the native creation must ask the process gate; nothing else in this flow asks it, "+
			"so an unchanged counter means the creation seam is not wired")
	require.GreaterOrEqual(t, gate.Admitted, uint64(1))
	require.Zero(t, gate.Refused, "a single flow must not be refused")
	require.Equal(t, outbound.NativeCreationStats().Attempts, gate.Requested,
		"one gate request per native creation attempt")
}

// TestARealH3AttemptFallsBackToH2 drives the opt-in fallback against the real engine: the inbound
// fixture serves HTTP/2 only, so the forced-H3 attempt must fail at the transport and the same flow
// must be retried over HTTP/2 and carry bytes.
func TestARealH3AttemptFallsBackToH2(t *testing.T) {
	echo := startNaiveCarrierEcho(t)
	router := &naiveCarrierRouter{target: echo}
	port, certificate := startNaiveCarrierInbound(t, router)
	outbound := newNaiveCarrierOutboundWithGuard(t, port, certificate, nil, true)

	conn := naiveRoundTrip(t, outbound, "after-the-real-h3-fallback")
	_ = conn.Close()
	require.EqualValues(t, 1, router.streams.Load(), "the fallback flow reached the inbound")

	// The fallback is a second ATTEMPT of the same logical flow, and the creation ledger keeps
	// them apart: at least one creation happened, and the ledger still balances.
	stats := outbound.NativeCreationStats()
	require.GreaterOrEqual(t, stats.Created, uint64(1))
	require.Equal(t, stats.Attempts, stats.Accounted())
	require.Zero(t, stats.InFlight)
}
