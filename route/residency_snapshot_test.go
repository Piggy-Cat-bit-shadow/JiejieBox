package route

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/httpclient"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The residency snapshot is the inventory half of the retirement walks, and these tests pin what it
// may and may not claim.
//
// # What it must claim
//
// The owner set of the two walks, split by kind, with the number of owners that answer the release
// capability. It must be the SAME set the walks act on - the walks and the report cannot disagree
// about who is reachable - and it must be a pure read: taking a snapshot may not close, drain or dial
// anything, and may not move any owner's counters.
//
// # What it must NOT claim
//
// Resource counts. No test here may assert on connections, streams or generations, because the
// snapshot does not see them; the per-owner ledger is covered in the pool's own package. The
// distinction is the reason the previous walk's single integer was misread, so it is stated in the
// type and pinned by asserting that the fake owners' own counters stay untouched.

// plainResidencyOutbound is a reachable owner that does NOT implement the release capability. Most
// outbounds are this shape: they own no reusable pool, so the walk reaches nothing they cannot answer.
type plainResidencyOutbound struct {
	adapter.Outbound
	tag string
}

func (o *plainResidencyOutbound) Tag() string            { return o.tag }
func (o *plainResidencyOutbound) Type() string           { return "plain-residency" }
func (o *plainResidencyOutbound) Network() []string      { return nil }
func (o *plainResidencyOutbound) Dependencies() []string { return nil }

// plainResidencyEndpoint and plainResidencyDNSTransport are the same shape on the other two lists.
type plainResidencyEndpoint struct {
	adapter.Endpoint
}

func (e *plainResidencyEndpoint) Tag() string  { return "plain-residency-endpoint" }
func (e *plainResidencyEndpoint) Type() string { return "plain-residency-endpoint" }

type plainResidencyDNSTransport struct {
	adapter.DNSTransport
}

func (t *plainResidencyDNSTransport) Tag() string  { return "plain-residency-dns" }
func (t *plainResidencyDNSTransport) Type() string { return "plain-residency-dns" }

// residencyManagers builds one releasable and one non-releasable owner of every kind, so the counts
// are distinguishable from a plain "all owners" or "all releasable" number.
func residencyManagers(outboundKeeper, dnsKeeper *reuseTestKeeper, httpKeeper *reuseTestKeeper) func(context.Context) context.Context {
	return func(ctx context.Context) context.Context {
		ctx = service.ContextWith[adapter.OutboundManager](ctx, &reuseTestOutboundManager{
			outbounds: []adapter.Outbound{
				outboundKeeper,
				&plainResidencyOutbound{tag: "plain"},
			},
		})
		ctx = service.ContextWith[adapter.EndpointManager](ctx, &reuseTestEndpointManager{
			endpoints: []adapter.Endpoint{
				&coverageTestEndpoint{keeper: &reuseTestKeeper{}},
				&plainResidencyEndpoint{},
			},
		})
		ctx = service.ContextWith[adapter.DNSTransportManager](ctx, &reuseTestDNSTransportManager{
			transports: []adapter.DNSTransport{
				&reuseTestDNSTransport{keeper: dnsKeeper},
				&plainResidencyDNSTransport{},
			},
		})
		return service.ContextWith[adapter.HTTPClientManager](ctx, &reuseTestHTTPClient{keeper: httpKeeper})
	}
}

// TestResidencySnapshotCountsTheWalkedOwnersPerKind pins the per-kind inventory against the walk's
// own owner set: two outbounds, two endpoints, two DNS transports and one HTTP client manager, with
// the release capability on exactly one of each pair.
func TestResidencySnapshotCountsTheWalkedOwnersPerKind(t *testing.T) {
	outboundKeeper := &reuseTestKeeper{}
	dnsKeeper := &reuseTestKeeper{}
	httpKeeper := &reuseTestKeeper{}
	manager, _ := newReuseTestManager(t, outboundKeeper, dnsKeeper,
		residencyManagers(outboundKeeper, dnsKeeper, httpKeeper))

	snapshot := manager.ResidencySnapshot()

	require.Equal(t, adapter.ResidencyOwnerCount{Reachable: 2, Releasable: 1}, snapshot.Outbounds)
	require.Equal(t, adapter.ResidencyOwnerCount{Reachable: 2, Releasable: 1}, snapshot.Endpoints)
	require.Equal(t, adapter.ResidencyOwnerCount{Reachable: 2, Releasable: 1}, snapshot.DNSTransports)
	require.Equal(t, adapter.ResidencyOwnerCount{Reachable: 1, Releasable: 1}, snapshot.HTTPClients)
}

// TestResidencySnapshotIsAPureRead pins the "inventory, not action" contract: a snapshot must not
// move any of the counters a walk moves, or a report read would retire pools behind the policy's back.
func TestResidencySnapshotIsAPureRead(t *testing.T) {
	outboundKeeper := &reuseTestKeeper{idleConns: 1}
	dnsKeeper := &reuseTestKeeper{idleConns: 1}
	httpKeeper := &reuseTestKeeper{idleConns: 1}
	manager, _ := newReuseTestManager(t, outboundKeeper, dnsKeeper,
		residencyManagers(outboundKeeper, dnsKeeper, httpKeeper))

	outboundKeeper.resetCounters()
	dnsKeeper.resetCounters()
	httpKeeper.resetCounters()

	_ = manager.ResidencySnapshot()

	for name, keeper := range map[string]*reuseTestKeeper{
		"outbound": outboundKeeper,
		"dns":      dnsKeeper,
		"http":     httpKeeper,
	} {
		idle, _, _, dials, retires, idleClosed := keeper.snapshot()
		require.Equal(t, 1, idle, "%s: a snapshot closed an idle connection", name)
		require.Zero(t, dials, "%s: a snapshot dialled", name)
		require.Zero(t, retires, "%s: a snapshot called the retire action", name)
		require.Zero(t, idleClosed, "%s: a snapshot released a connection", name)
	}
}

// TestResidencySnapshotWithoutManagersIsZero keeps the report safe on a context with no managers: a
// bare manager (a test, or a box assembled without them) reports zeros rather than panicking.
func TestResidencySnapshotWithoutManagersIsZero(t *testing.T) {
	manager := &ReferenceManager{ctx: context.Background()}
	require.Equal(t, adapter.ResidencySnapshot{}, manager.ResidencySnapshot())
}

// TestResidencySnapshotIsRegisteredAsTheReportCapability pins the production wiring: a started
// reference manager must make itself reachable as adapter.ResidencyReporter, because that is the
// only handle service/oomkiller has on it.
func TestResidencySnapshotIsRegisteredAsTheReportCapability(t *testing.T) {
	outboundKeeper := &reuseTestKeeper{}
	dnsKeeper := &reuseTestKeeper{}
	manager, _ := newReuseTestManager(t, outboundKeeper, dnsKeeper)

	reporter := service.FromContext[adapter.ResidencyReporter](manager.ctx)
	require.NotNil(t, reporter, "a started reference manager must register the residency reporter")
	require.Equal(t, manager, reporter)
	require.Equal(t, manager.ResidencySnapshot(), reporter.ResidencySnapshot())
}

// TestTheReleaseCapabilityIsWhatMakesAnOwnerReleasable is the red-check on the capability itself,
// on the PRODUCTION HTTP client manager rather than on a double: if the manager ever stops
// implementing IdleConnectionReleaser, the snapshot's releasable count for it drops to zero and the
// walk silently skips the pool again.
func TestTheReleaseCapabilityIsWhatMakesAnOwnerReleasable(t *testing.T) {
	var productionManager *httpclient.Manager
	require.Implements(t, (*adapter.IdleConnectionReleaser)(nil), productionManager)
}
