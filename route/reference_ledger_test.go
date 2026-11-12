package route

// Q15: the object-identity ledger, aggregated over the same walk the retire policy uses.
//
// The point of these tests is the part that is easy to get wrong and expensive to get wrong: an
// owner must be visited ONCE (so its objects are summed once), and an owner that cannot answer must
// be counted as UNKNOWN rather than as zero objects. Both are asserted against the shared walk, not
// against a copy of it.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// ledgerKeeper is a reusable owner that can answer with an object ledger.
type ledgerKeeper struct {
	reuseTestKeeper

	ledger adapter.ResidencyLedger
	// answers counts how many times the walk asked. It must be one.
	answers int
}

func (k *ledgerKeeper) ResidencyLedger() adapter.ResidencyLedger {
	k.answers++
	return k.ledger
}

// newLedgerFixture builds a reference manager over fake managers whose owners are the given ones.
func newLedgerFixture(t *testing.T, outbounds []adapter.Outbound, transports []adapter.DNSTransport) *ReferenceManager {
	t.Helper()
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), &reuseTestOutboundManager{outbounds: outbounds})
	ctx = service.ContextWith[adapter.EndpointManager](ctx, &reuseTestEndpointManager{})
	ctx = service.ContextWith[adapter.InboundManager](ctx, &reuseTestInboundManager{})
	ctx = service.ContextWith[adapter.ServiceManager](ctx, &reuseTestServiceManager{})
	ctx = service.ContextWith[adapter.DNSTransportManager](ctx, &reuseTestDNSTransportManager{transports: transports})
	ctx = service.ContextWith[adapter.NetworkManager](ctx, &reuseTestNetworkManager{})
	return NewReferenceManager(ctx, log.NewNOPFactory().NewLogger("residency"), option.Options{})
}

// TestTheObjectLedgerSumsEachOwnerOnceAndReportsCoverage is the aggregation contract.
func TestTheObjectLedgerSumsEachOwnerOnceAndReportsCoverage(t *testing.T) {
	owner := &ledgerKeeper{
		reuseTestKeeper: reuseTestKeeper{tag: "a"},
		ledger: adapter.ResidencyLedger{
			Creating:   1,
			Active:     2,
			Idle:       3,
			Draining:   1,
			Terminated: 9,
			Exact:      true,
			Answered:   true,
			Note:       "test owner",
		},
	}
	// An owner of the same kind that cannot answer. It must count as UNKNOWN, never as zero.
	silent := &reuseTestKeeper{tag: "b"}

	reference := newLedgerFixture(t, []adapter.Outbound{owner, silent}, nil)
	snapshot := reference.ObjectResidencySnapshot()

	outbounds := snapshot.Outbounds
	require.Equal(t, 2, outbounds.Owners)
	require.Equal(t, 1, outbounds.Reporting)
	require.Equal(t, 1, outbounds.Unknown,
		"an owner that cannot answer must be reported as unknown, not as zero objects")
	require.Zero(t, outbounds.Estimated)
	require.Equal(t, uint64(1), outbounds.Creating)
	require.Equal(t, uint64(2), outbounds.Active)
	require.Equal(t, uint64(3), outbounds.Idle)
	require.Equal(t, uint64(1), outbounds.Draining)
	require.Equal(t, uint64(9), outbounds.Terminated)
	require.Equal(t, 1, owner.answers, "the walk asked one owner more than once: its objects would double count")

	// A kind with no owners is zero coverage, not an error.
	require.Zero(t, snapshot.Endpoints.Owners)
	require.Zero(t, snapshot.HTTPClients.Owners)
}

// TestAnEstimatedLedgerIsReportedAsEstimated keeps the unknown boundary honest at the owner level: a
// pool that can only estimate must not be summed into the exact total silently.
func TestAnEstimatedLedgerIsReportedAsEstimated(t *testing.T) {
	estimated := &ledgerKeeper{
		reuseTestKeeper: reuseTestKeeper{tag: "estimated"},
		ledger: adapter.ResidencyLedger{
			Active:   4,
			Exact:    false,
			Answered: true,
			Note:     "native objects have no observation interface",
		},
	}
	exact := &ledgerKeeper{
		reuseTestKeeper: reuseTestKeeper{tag: "exact"},
		ledger:          adapter.ResidencyLedger{Idle: 1, Exact: true, Answered: true},
	}
	transport := &ledgerDNSTransport{owner: &ledgerKeeper{
		reuseTestKeeper: reuseTestKeeper{tag: "dns"},
		ledger:          adapter.ResidencyLedger{Idle: 2, Exact: true, Answered: true},
	}}

	reference := newLedgerFixture(t, []adapter.Outbound{estimated, exact}, []adapter.DNSTransport{transport})
	snapshot := reference.ObjectResidencySnapshot()

	require.Equal(t, 2, snapshot.Outbounds.Owners)
	require.Equal(t, 2, snapshot.Outbounds.Reporting)
	require.Equal(t, 1, snapshot.Outbounds.Estimated,
		"the estimated ledger must be visible as coverage, not hidden inside the total")
	require.Equal(t, uint64(4), snapshot.Outbounds.Active)
	require.Equal(t, uint64(1), snapshot.Outbounds.Idle)

	require.Equal(t, 1, snapshot.DNSTransports.Owners)
	require.Equal(t, 1, snapshot.DNSTransports.Reporting)
	require.Equal(t, uint64(2), snapshot.DNSTransports.Idle,
		"a different kind was not summed into its own record")
}

// TestAReporterThatCannotAnswerIsStillUnknown is the answered boundary: implementing the interface
// is not the same as being able to count. An owner that returns Answered=false has its zero values
// read as "cannot say", never as "holds zero objects".
func TestAReporterThatCannotAnswerIsStillUnknown(t *testing.T) {
	silent := &ledgerKeeper{
		reuseTestKeeper: reuseTestKeeper{tag: "silent"},
		ledger: adapter.ResidencyLedger{
			Answered: false,
			Note:     "the dependency owns the objects and exposes no observation interface",
		},
	}
	reference := newLedgerFixture(t, []adapter.Outbound{silent}, nil)
	snapshot := reference.ObjectResidencySnapshot()
	require.Equal(t, 1, snapshot.Outbounds.Owners)
	require.Zero(t, snapshot.Outbounds.Reporting)
	require.Equal(t, 1, snapshot.Outbounds.Unknown,
		"an unanswered ledger must not be summed as a precise zero")
	require.Zero(t, snapshot.Outbounds.Active)
}

// TestTheObjectLedgerKeepsUnlikeUnitsApart is the audit's Q15 unit rule: a transport that can only
// count streams and one that counts sessions are both outbounds, and their Active counts must never
// be presented as one number - that is the "stream/session/device in one physical connection count"
// the finding names.
func TestTheObjectLedgerKeepsUnlikeUnitsApart(t *testing.T) {
	streams := &ledgerKeeper{
		reuseTestKeeper: reuseTestKeeper{tag: "streams"},
		ledger: adapter.ResidencyLedger{
			Unit:     adapter.ResidencyUnitStream,
			Active:   3,
			Exact:    false,
			Answered: true,
			Note:     "the library owns the pooled connections; this owner can only see streams",
		},
	}
	sessions := &ledgerKeeper{
		reuseTestKeeper: reuseTestKeeper{tag: "sessions"},
		ledger: adapter.ResidencyLedger{
			Unit:     adapter.ResidencyUnitSession,
			Active:   2,
			Exact:    true,
			Answered: true,
		},
	}
	unname := &ledgerKeeper{
		reuseTestKeeper: reuseTestKeeper{tag: "unname"},
		ledger:          adapter.ResidencyLedger{Idle: 1, Exact: true, Answered: true},
	}

	reference := newLedgerFixture(t, []adapter.Outbound{streams, sessions, unname}, nil)
	snapshot := reference.ObjectResidencySnapshot()

	require.True(t, snapshot.Outbounds.MixedUnits, "the kind's flat totals span three units and must say so")
	require.Len(t, snapshot.Outbounds.Units, 3)
	units := make(map[adapter.ResidencyUnit]adapter.ResidencyUnitCounts, len(snapshot.Outbounds.Units))
	for _, unit := range snapshot.Outbounds.Units {
		units[unit.Unit] = unit
	}
	require.EqualValues(t, 3, units[adapter.ResidencyUnitStream].Active)
	require.EqualValues(t, 2, units[adapter.ResidencyUnitSession].Active)
	require.EqualValues(t, 1, units[adapter.ResidencyUnitUnspecified].Idle,
		"an owner that names no unit gets its own record instead of joining a named one")
	require.False(t, units[adapter.ResidencyUnitStream].Exact, "an estimate stays visible per unit")
	require.True(t, units[adapter.ResidencyUnitSession].Exact)
}

// TestTheObjectLedgerCountsGenerationsAcrossOwners is the cross-generation half: a pool that retired a
// generation while a request was still running holds TWO generations, and the retired one is Draining
// - not a number that disappeared.
func TestTheObjectLedgerCountsGenerationsAcrossOwners(t *testing.T) {
	pool := &ledgerKeeper{
		reuseTestKeeper: reuseTestKeeper{tag: "pool"},
		ledger: adapter.ResidencyLedger{
			Unit:               adapter.ResidencyUnitGeneration,
			Active:             1,
			Draining:           1,
			Generation:         4,
			RetiredGenerations: 1,
			Terminated:         7,
			Exact:              true,
			Answered:           true,
		},
	}
	fresh := &ledgerKeeper{
		reuseTestKeeper: reuseTestKeeper{tag: "fresh"},
		ledger: adapter.ResidencyLedger{
			Unit:       adapter.ResidencyUnitGeneration,
			Idle:       1,
			Generation: 1,
			Exact:      true,
			Answered:   true,
		},
	}
	reference := newLedgerFixture(t, []adapter.Outbound{pool, fresh}, nil)
	snapshot := reference.ObjectResidencySnapshot()

	require.Equal(t, 3, snapshot.Outbounds.Generations,
		"two owners holding three generation instances: one current each, plus the retired one")
	require.Equal(t, 1, snapshot.Outbounds.RetiredGenerations)
	require.Equal(t, uint64(1), snapshot.Outbounds.Active)
	require.Equal(t, uint64(1), snapshot.Outbounds.Draining)
	require.Equal(t, uint64(1), snapshot.Outbounds.Idle)
	require.False(t, snapshot.Outbounds.MixedUnits, "both owners count generations")

	units := snapshot.Outbounds.Units
	require.Len(t, units, 1)
	require.Equal(t, adapter.ResidencyUnitGeneration, units[0].Unit)
	require.EqualValues(t, 4, units[0].Generation, "the maximum generation ordinal reported, not a shared clock")
	require.Equal(t, 3, units[0].Generations)
}

// ledgerDNSTransport is a DNS transport owner that answers with a ledger.
type ledgerDNSTransport struct {
	reuseTestDNSTransport

	owner *ledgerKeeper
}

func (t *ledgerDNSTransport) ResidencyLedger() adapter.ResidencyLedger {
	return t.owner.ResidencyLedger()
}
func (t *ledgerDNSTransport) SetKeepIdleConnections(bool) {}
func (t *ledgerDNSTransport) CloseIdleConnections()       {}
