package route

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/service/powerreport"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// A running power report is an accounting consumer, and accounting must not be lost to an optimisation.
//
// # Why this is not covered by the tracker rule
//
// The tracker refusal is about a tracker the CONFIGURATION attached. The power report is installed by
// the build on every userspace connection, whether or not any rule mentions it: a bypassed flow would
// never be counted, and the report would undercount exactly the sessions where the fast path was
// eligible - which is the hardest kind of gap to notice, because the report looks plausible.
//
// The repo already states the principle for the other consumer ("silently losing accounting is not an
// acceptable optimisation"). This pins it for both.
func TestARunningPowerReportRefusesTheFastPath(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	router, outbound := offloadRouter(false)
	chain := []adapter.Outbound{outbound}
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	// Control: with no recorder running, this is the shape the fast path exists for.
	require.True(t, router.canFastBypass(&metadata, destination, chain, outbound, false).BypassAllowed(),
		"the fixture must be eligible, or this test proves nothing about the recorder")

	// A REGISTERED manager with no recorder is not a reason to refuse: every build registers the
	// service, and only an active recorder counts anything.
	router.powerReport = powerreport.NewManager()
	require.True(t, router.canFastBypass(&metadata, destination, chain, outbound, false).BypassAllowed(),
		"a manager with no recorder counts nothing, so it cannot lose anything")

	require.NoError(t, router.powerReport.Start(powerreport.Options{BasePath: t.TempDir()}))
	require.Equal(t, BypassRefusedPowerReport,
		router.canFastBypass(&metadata, destination, chain, outbound, false),
		"a flow that is never counted must not be bypassed: the refusal has to name the reason, because "+
			"'my report is short' is otherwise indistinguishable from a rules problem")
	require.Contains(t, BypassRefusedPowerReport.String(), "power report",
		"and the verdict's spelling is what an operator reads")

	require.NoError(t, router.powerReport.Close())
	require.True(t, router.canFastBypass(&metadata, destination, chain, outbound, false).BypassAllowed(),
		"and once reporting stops, the flow is eligible again - the guard follows the recorder, not the "+
			"existence of the feature")
}

// TestThePowerReportRefusalKeepsTheOrderOfReasons pins that a flow refused earlier keeps its own reason.
//
// Reporting the power report for a sniffed-domain flow would be misleading: stopping the report would
// not make it eligible. The verdict is the FIRST condition that failed, and this pins that the new
// condition was inserted beside the tracker for that reason rather than in front of the allow-list.
func TestThePowerReportRefusalKeepsTheOrderOfReasons(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	router, outbound := offloadRouter(false)
	chain := []adapter.Outbound{outbound}

	router.powerReport = powerreport.NewManager()
	require.NoError(t, router.powerReport.Start(powerreport.Options{BasePath: t.TempDir()}))
	defer router.powerReport.Close()

	sniffed := fastBypassMetadata(N.NetworkTCP, destination)
	sniffed.Domain = "szminorshort.weixin.qq.com"
	require.Equal(t, BypassRefusedSniffedDomain, router.canFastBypass(&sniffed, destination, chain, outbound, false),
		"a flow refused for a structural reason must keep that reason")
}

// TestTheRouterTakesThePowerReportFromTheServiceContext is the WIRING, which the tests above cannot see.
//
// They set `router.powerReport` directly, so deleting the line in `NewRouter` that resolves it from the
// service registry would leave every one of them green while the guard did nothing in production. That is the
// shape this campaign exists for - a repair that is correct and unreachable - so the wiring is asserted on the
// object the constructor produces.
func TestTheRouterTakesThePowerReportFromTheServiceContext(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	manager := powerreport.NewManager()
	service.MustRegister[*powerreport.Manager](ctx, manager)

	router := NewRouter(ctx, log.NewNOPFactory(), option.RouteOptions{}, option.DNSOptions{})
	require.Same(t, manager, router.powerReport,
		"the router must read the power report service from its context: the guard is worthless if the field "+
			"is only ever set by a test")

	// And the end-to-end consequence on that router: a registered manager with no recorder permits, an active
	// recorder refuses.
	destination := M.ParseSocksaddr("93.184.216.34:443")
	outbound := newProductionDirectOutbound()
	router.outbound = &stubOutboundManager{def: outbound}
	chain := []adapter.Outbound{outbound}
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	require.True(t, router.canFastBypass(&metadata, destination, chain, outbound, false).BypassAllowed(),
		"a registered manager with no recorder counts nothing")
	require.NoError(t, manager.Start(powerreport.Options{BasePath: t.TempDir()}))
	defer manager.Close()
	require.Equal(t, BypassRefusedPowerReport,
		router.canFastBypass(&metadata, destination, chain, outbound, false),
		"and an active recorder on the REAL wiring refuses")
}
