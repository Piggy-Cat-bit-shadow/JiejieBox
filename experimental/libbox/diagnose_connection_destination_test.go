package libbox

// P1.2 — the active diagnostic must dial the destination the ROUTE DECISION chose, and must not
// pretend a literal target has a DNS decision.
//
// The reviewed failure: a rule rewrote port 443 to 8080 and the test still dialled 443, reporting
// "connected via direct (route decision)". The decision was explained but never used.

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/common/physicalpath"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// destinationRecordingPath previews a decided route with a rewritten destination.
type destinationRecordingPath struct {
	*fakeConnectionTestPath
	preview connectionTestRulePreview
}

func (p *destinationRecordingPath) PreviewRouteRule(string, string, uint16) (connectionTestRulePreview, bool) {
	return p.preview, true
}

func TestConnectionTestDialsTheRouteDecisionsDestination(t *testing.T) {
	var dialed atomic.Pointer[M.Socksaddr]
	member := &fakeConnectionOutbound{tag: "direct", behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		recorded := destination
		dialed.Store(&recorded)
		return stubConn{}, nil
	}}
	base := newConnectionTestPath("direct", member)
	base.status = physicalpath.PathStatus{Root: "direct", Network: "tcp", Exit: "direct"}
	path := &destinationRecordingPath{
		fakeConnectionTestPath: base,
		preview: connectionTestRulePreview{
			Matched:    true,
			RuleIndex:  0,
			Rule:       "port:443",
			ActionType: "route",
			Outbound:   "direct",
			// What the route action's override_port produced.
			FinalDestination: M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), 8080),
		},
	}
	report := runConnectionTest(context.Background(), path, "", "tcp", "http://127.0.0.1:443/path")

	require.Equal(t, connectionTestOutcomeOK, report.Outcome)
	recorded := dialed.Load()
	require.NotNil(t, recorded)
	require.Equal(t, "127.0.0.1:8080", recorded.String(),
		"the dial must use the destination the route decision produced, not the URL's original port")
	stages := stagesByID(report)
	require.Contains(t, stages[connectionTestStageResolve].Detail, "8080")
	require.Contains(t, stages[connectionTestStageDial].Detail, "8080")
}

// TestConnectionTestSaysNoDNSForALiteralTarget: a literal address never reaches a DNS rule, so the
// DNS stage must say so instead of naming a default server.
func TestConnectionTestSaysNoDNSForALiteralTarget(t *testing.T) {
	var dnsPreviews atomic.Int32
	member := &fakeConnectionOutbound{tag: "direct", behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		return stubConn{}, nil
	}}
	path := &dnsPreviewingCounterPath{
		fakeConnectionTestPath: newConnectionTestPath("direct", member),
		calls:                  &dnsPreviews,
	}
	report := runConnectionTest(context.Background(), path, "direct", "tcp", "http://127.0.0.1:443/path")

	stages := stagesByID(report)
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageDNS].Status)
	require.Contains(t, stages[connectionTestStageDNS].Detail, "literal address")
	require.Zero(t, dnsPreviews.Load(), "a literal target must not even ask the DNS preview")
}

// TestConnectionTestSaysNoDNSWhenTheRouteRewroteToALiteral is the same rule after a rewrite.
func TestConnectionTestSaysNoDNSWhenTheRouteRewroteToALiteral(t *testing.T) {
	member := &fakeConnectionOutbound{tag: "direct", behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		return stubConn{}, nil
	}}
	path := &destinationRecordingPath{
		fakeConnectionTestPath: newConnectionTestPath("direct", member),
		preview: connectionTestRulePreview{
			Matched:          true,
			RuleIndex:        0,
			ActionType:       "route",
			Outbound:         "direct",
			FinalDestination: M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), 8080),
		},
	}
	report := runConnectionTest(context.Background(), path, "", "tcp", "http://example.com:443/path")
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageDNS].Status)
	require.Contains(t, stages[connectionTestStageDNS].Detail, "rewrote the destination")
}

// TestConnectionTestRefusesANonTCPRequest: the entry is a TCP connect and must say so.
func TestConnectionTestRefusesANonTCPRequest(t *testing.T) {
	member := &fakeConnectionOutbound{tag: "direct", behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		t.Fatal("a non-TCP request must not dial")
		return nil, nil
	}}
	report := runConnectionTest(context.Background(), newConnectionTestPath("direct", member), "direct", "udp", "http://127.0.0.1:443/path")
	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageFailed, stages[connectionTestStagePath].Status)
	require.Contains(t, stages[connectionTestStagePath].Detail, "can only test TCP")
}

// dnsPreviewingCounterPath counts DNS preview calls and can also resolve domains.
type dnsPreviewingCounterPath struct {
	*fakeConnectionTestPath
	calls *atomic.Int32
}

func (p *dnsPreviewingCounterPath) PreviewDNSRule(string, string, uint16) (connectionTestDNSPreview, bool) {
	p.calls.Add(1)
	return connectionTestDNSPreview{Matched: false}, true
}
