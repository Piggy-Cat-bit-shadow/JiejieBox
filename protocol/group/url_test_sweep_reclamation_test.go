package group

// E3 — what a full URL-test sweep costs, and what is actually reclaimed afterwards.
//
// # The scenario
//
// The user asked what happens to resources after a "test all" over a large node set. This test
// drives the sweep at the level where a sweep IS a unit: `URLTestGroup.URLTest` is the synchronous
// entry point behind the manual/group test, it owns its round, and it returns only after every
// probe has been joined and every result has been published.
//
// It deliberately does NOT use `PostStart`: that also fires one unforced round on its own, which on
// Linux races the startup network-environment transition (probes refused as "network changed") and
// makes the FIRST sweep's presence a host-timing artefact. The sweep under test is the explicit one;
// the Box-level wiring (history cleared by Instance.Close) is covered by
// `daemon/url_test_sweep_reclamation_test.go`.
//
// # What this establishes
//
//	per-probe       every member is measured once per sweep against one target, and every probe's
//	                connection is closed when the sweep ends (the target's own ConnState ledger)
//	per-sweep       the history does NOT grow with the number of sweeps: N members produce N health
//	                entries and N display entries after one sweep and still N after a second one.
//	                There is no per-sweep trim of history, and this test does not pretend there is:
//	                a measurement result is the product of the sweep, so retaining it is the
//	                contract, not a leak
//	transient       the shared-measurement registry and the Box-wide round admission return to zero:
//	                entries exist only while participants are present
//	teardown        `HistoryStorage.Close` releases every measurement and refuses later writes - the
//	                storage half of the reclamation `Instance.Close` performs
//
// The test does NOT assert resident memory: Go's heap is not attributable from inside the process,
// and a number like that would be a vacuous measurement.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// crossSystemSweepMembers is the number of members one sweep measures. Two sweeps are performed, so
// the test pushes two thousand measurements through the real measurement engine and storage API.
const crossSystemSweepMembers = 1000

// sweepTargetOutbound is a leaf that dials the real local target. It is a plain TCP dialer, so the
// measurement engine runs its full path (dial, transport, HTTP request) instead of a stub's.
type sweepTargetOutbound struct {
	adapter.Outbound
	tag string
}

func (o *sweepTargetOutbound) Type() string      { return "sweep-target" }
func (o *sweepTargetOutbound) Tag() string       { return o.tag }
func (o *sweepTargetOutbound) Network() []string { return []string{N.NetworkTCP, N.NetworkUDP} }

func (o *sweepTargetOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, destination.String())
}

func (o *sweepTargetOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// sweepConnectionLedger counts the target's connection states, so "the probe connections were
// reclaimed" is a fact about sockets rather than an inference from the round returning.
type sweepConnectionLedger struct {
	opened   atomic.Int32
	closed   atomic.Int32
	hijacked atomic.Int32
}

func (l *sweepConnectionLedger) open() int32 { return l.opened.Load() }
func (l *sweepConnectionLedger) shut() int32 { return l.closed.Load() + l.hijacked.Load() }
func (l *sweepConnectionLedger) live() int32 { return l.open() - l.shut() }

func newSweepTarget(t *testing.T) (*httptest.Server, *sweepConnectionLedger, *atomic.Int64) {
	t.Helper()
	ledger := &sweepConnectionLedger{}
	var requests atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			ledger.opened.Add(1)
		case http.StateClosed:
			ledger.closed.Add(1)
		case http.StateHijacked:
			ledger.hijacked.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	return server, ledger, &requests
}

func TestAFullURLTestSweepReclaimsItsTransientState(t *testing.T) {
	target, ledger, requests := newSweepTarget(t)

	members := make([]adapter.Outbound, crossSystemSweepMembers)
	for index := range members {
		members[index] = &sweepTargetOutbound{tag: fmt.Sprintf("n%04d", index)}
	}

	history := urltest.NewHistoryStorage()
	ctx := service.ContextWithPtr(context.Background(), history)
	share := urltest.NewMeasurementShare()
	ctx = urltest.ContextWithMeasurementShare(ctx, share)
	coordinator := urltest.NewCoordinator(10)
	ctx = urltest.ContextWithCoordinator(ctx, coordinator)
	sweepGroup, err := NewURLTestGroup(
		ctx,
		&stubOutboundManager{},
		log.NewNOPFactory().NewLogger("sweep"),
		members,
		target.URL,
		0, 0, time.Hour,
		false,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sweepGroup.Close() })

	sweep := func(minimumRequests int64) {
		t.Helper()
		// The synchronous entry point: it owns one round and returns only after the round is
		// joined, so the assertions after this call are about a COMPLETED sweep.
		result, sweepErr := sweepGroup.URLTest(context.Background())
		require.NoError(t, sweepErr)
		require.Len(t, result, crossSystemSweepMembers,
			"every member must have a result: a full sweep measures all of them")
		require.GreaterOrEqual(t, requests.Load(), minimumRequests,
			"the target did not see one request per member: the sweep was not full")
		require.Equal(t, crossSystemSweepMembers, history.HealthEntryCount(),
			"a full sweep must leave exactly one health entry per (member,target)")
		require.Equal(t, crossSystemSweepMembers, history.DisplayEntryCount(),
			"a full sweep must leave one display entry per member")
		require.Zero(t, coordinator.RoundsInFlight(), "the round slot must be returned")
		require.Zero(t, coordinator.InFlight())
		require.Zero(t, share.InFlight(), "the shared-measurement registry must be empty after a sweep")
		require.Zero(t, share.Participants())
	}

	// SWEEP 1.
	sweep(int64(crossSystemSweepMembers))
	firstSweepRequests := requests.Load()

	// Per-probe connection reclamation: each measurement owns one connection and closes it when the
	// measurement ends, so the target ends with as many closed connections as opened ones.
	require.Eventually(t, func() bool {
		return ledger.open() >= int32(crossSystemSweepMembers) && ledger.live() == 0
	}, 30*time.Second, 5*time.Millisecond,
		"probe connections were not reclaimed: opened=%d closed=%d", ledger.open(), ledger.shut())

	// SWEEP 2. The history does not grow with the number of sweeps: each sweep REPLACES the
	// per-(member,target) evidence instead of adding a second entry per member.
	sweep(firstSweepRequests + int64(crossSystemSweepMembers))
	require.GreaterOrEqual(t, requests.Load(), firstSweepRequests+int64(crossSystemSweepMembers),
		"the second sweep did not measure every member")
	require.Equal(t, crossSystemSweepMembers, history.HealthEntryCount(),
		"a second sweep must replace the evidence, not double it")
	require.Equal(t, crossSystemSweepMembers, history.DisplayEntryCount(),
		"a second sweep must replace the display evidence, not grow it")
	require.Eventually(t, func() bool {
		return ledger.live() == 0
	}, 30*time.Second, 5*time.Millisecond,
		"the second sweep left a probe connection open: opened=%d closed=%d", ledger.open(), ledger.shut())

	// THE RECLAMATION MECHANISM. Closing the storage is what actually drops the measurements; the
	// Box performs exactly this call in Instance.Close. There is no per-sweep trim: the results ARE
	// the product of the sweep, so they persist until their owner goes away.
	scope, scopeErr := urltest.NewMeasurementScope(target.URL, urltest.ExpectedStatus{})
	require.NoError(t, scopeErr)
	require.NoError(t, sweepGroup.Close())
	require.NoError(t, history.Close())
	require.Zero(t, history.HealthEntryCount(), "Close must release every health measurement")
	require.Zero(t, history.DisplayEntryCount(), "Close must release every display measurement")

	// A write after Close is refused rather than resurrecting state in a closed storage.
	history.StoreHealthHistory("post-close", scope, &adapter.URLTestHistory{Delay: 1, Time: time.Now()})
	history.StoreDisplayHistory("post-close", scope, &adapter.URLTestHistory{Delay: 1, Time: time.Now()})
	require.Zero(t, history.HealthEntryCount(), "a closed storage accepted a health write")
	require.Zero(t, history.DisplayEntryCount(), "a closed storage accepted a display write")

	// No probe connection survives teardown.
	require.Eventually(t, func() bool {
		return ledger.live() == 0
	}, 30*time.Second, 5*time.Millisecond,
		"probe connections survived teardown: opened=%d closed=%d", ledger.open(), ledger.shut())
}
