package libbox

// O02 — the bounded active connection test.
//
// These cases drive the runner with a fake path, so they can pin the things the entry is
// FOR without a live Box: the staged report shape with its stable stage ids, the stage that
// failed being the one reported, the dial being skipped when there is no exit to dial
// through, the caller's cancellation being reported as a cancellation rather than a verdict
// about the path, and both budgets actually bounding the work.
//
// A live-Box run is covered by the real CommandServer entry, which needs a running service
// and is therefore out of scope for this unit test (and for this environment).

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/physicalpath"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// fakeConnectionTestPath is the passive model plus the outbound namespace the runner reads.
type fakeConnectionTestPath struct {
	status   physicalpath.PathStatus
	outbound adapter.OutboundManager
}

func (p *fakeConnectionTestPath) Status(root string, network string) physicalpath.PathStatus {
	return p.status
}

func (p *fakeConnectionTestPath) Outbound() adapter.OutboundManager { return p.outbound }

// fakeOutboundManager answers Outbound(tag) from a fixed set.
type fakeOutboundManager struct {
	adapter.OutboundManager
	members         map[string]adapter.Outbound
	defaultOutbound adapter.Outbound
}

func (m *fakeOutboundManager) Default() adapter.Outbound { return m.defaultOutbound }

func (m *fakeOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	member, loaded := m.members[tag]
	return member, loaded
}

// fakeConnectionOutbound is one dialable member whose result the test decides.
type fakeConnectionOutbound struct {
	adapter.Outbound
	tag      string
	behavior func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error)
}

func (o *fakeConnectionOutbound) Type() string { return "fake" }
func (o *fakeConnectionOutbound) Tag() string  { return o.tag }

func (o *fakeConnectionOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if o.behavior == nil {
		return nil, nil
	}
	return o.behavior(ctx, network, destination)
}

// stubConn is a conn the runner only has to close.
type stubConn struct{ net.Conn }

func (stubConn) Close() error { return nil }

// newConnectionTestPath builds a path whose exit is `exit` and whose only outbound is the
// given one (or none when nil).
func newConnectionTestPath(exit string, member adapter.Outbound) *fakeConnectionTestPath {
	manager := &fakeOutboundManager{members: map[string]adapter.Outbound{}}
	if member != nil {
		manager.members[member.Tag()] = member
	}
	return &fakeConnectionTestPath{
		status:   physicalpath.PathStatus{Root: "proxy", Network: "tcp", Exit: exit},
		outbound: manager,
	}
}

// stagesByID indexes the report's stages.
func stagesByID(report connectionTestReport) map[string]connectionTestStage {
	stages := make(map[string]connectionTestStage, len(report.Stages))
	for _, stage := range report.Stages {
		stages[stage.ID] = stage
	}
	return stages
}

func TestConnectionTestRequiresARunningService(t *testing.T) {
	var nilServer *CommandServer
	report, err := nilServer.ConnectionTest("proxy", "tcp", "https://example.com")
	require.Error(t, err)
	require.Nil(t, report, "an error must not carry a partial result")

	_, err = (&CommandServer{}).ConnectionTest("proxy", "tcp", "https://example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not started")
}

func TestConnectionTestReportsAStagedSuccess(t *testing.T) {
	member := &fakeConnectionOutbound{
		tag: "node-a",
		behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
			require.Equal(t, N.NetworkTCP, network)
			require.Equal(t, "example.com", destination.Fqdn)
			require.EqualValues(t, 443, destination.Port)
			return stubConn{}, nil
		},
	}
	report := runConnectionTest(context.Background(), newConnectionTestPath("node-a", member), "proxy", "tcp", "https://example.com/path")

	require.Equal(t, connectionTestOutcomeOK, report.Outcome)
	stages := stagesByID(report)
	require.Len(t, report.Stages, 4)
	require.Equal(t, []string{connectionTestStagePath, connectionTestStageRules, connectionTestStageResolve, connectionTestStageDial},
		[]string{report.Stages[0].ID, report.Stages[1].ID, report.Stages[2].ID, report.Stages[3].ID},
		"the stage ids are part of the diagnostic contract and must stay stable")
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageRules].Status,
		"this path cannot explain the rule, so the stage is skipped with a reason instead of guessing")
	require.NotEmpty(t, stages[connectionTestStageRules].Detail)
	require.Equal(t, connectionTestStageOK, stages[connectionTestStagePath].Status)
	require.Contains(t, stages[connectionTestStagePath].Detail, "node-a")
	require.Equal(t, "example.com:443", stages[connectionTestStageResolve].Detail)
	require.Equal(t, connectionTestStageOK, stages[connectionTestStageDial].Status)
	require.Contains(t, stages[connectionTestStageDial].Detail, "node-a")
}

func TestConnectionTestReportsTheFailingStage(t *testing.T) {
	member := &fakeConnectionOutbound{
		tag: "node-a",
		behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
			return nil, context.DeadlineExceeded
		},
	}
	report := runConnectionTest(context.Background(), newConnectionTestPath("node-a", member), "proxy", "tcp", "https://example.com")

	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageOK, stages[connectionTestStagePath].Status,
		"the passive model did name an exit, so the path stage did not fail")
	require.Equal(t, connectionTestStageFailed, stages[connectionTestStageDial].Status,
		"the stage that failed must be the one reported as failing")
	require.Contains(t, stages[connectionTestStageDial].Detail, "deadline exceeded")
}

func TestConnectionTestSkipsTheDialWhenThePathHasNoExit(t *testing.T) {
	report := runConnectionTest(context.Background(), newConnectionTestPath("", nil), "proxy", "tcp", "https://example.com")

	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageFailed, stages[connectionTestStagePath].Status)
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageDial].Status,
		"with no exit there is nothing to dial, and a skipped stage says that instead of pretending to try")
}

func TestConnectionTestSkipsTheDialWhenTheExitIsNotAnOutbound(t *testing.T) {
	report := runConnectionTest(context.Background(), newConnectionTestPath("missing-node", nil), "proxy", "tcp", "https://example.com")

	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageDial].Status)
	require.Contains(t, stages[connectionTestStageDial].Detail, "missing-node")
}

func TestConnectionTestRejectsANonHTTPTarget(t *testing.T) {
	member := &fakeConnectionOutbound{tag: "node-a"}
	report := runConnectionTest(context.Background(), newConnectionTestPath("node-a", member), "proxy", "tcp", "socks5://example.com")

	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageFailed, stages[connectionTestStageResolve].Status)
	require.Contains(t, stages[connectionTestStageResolve].Detail, "unsupported target scheme")
	require.NotContains(t, stages, connectionTestStageDial,
		"a target that cannot be resolved must not be dialled")
}

func TestConnectionTestReportsTheCallerCancellationAsCancellation(t *testing.T) {
	member := &fakeConnectionOutbound{
		tag: "node-a",
		behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	callerCtx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	report := runConnectionTest(callerCtx, newConnectionTestPath("node-a", member), "proxy", "tcp", "https://example.com")

	require.Equal(t, connectionTestOutcomeCancelled, report.Outcome,
		"a caller that gave up is not a verdict about the path")
	require.Contains(t, stagesByID(report)[connectionTestStageDial].Detail, "cancelled")
}

func TestConnectionTestIsBoundedByTheStageBudget(t *testing.T) {
	previous := connectionTestStageTimeout
	connectionTestStageTimeout = 100 * time.Millisecond
	t.Cleanup(func() { connectionTestStageTimeout = previous })

	member := &fakeConnectionOutbound{
		tag: "node-a",
		behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
			// Ignore the context entirely: only the runner's own budget can end this.
			time.Sleep(2 * time.Second)
			return stubConn{}, nil
		},
	}
	started := time.Now()
	report := runConnectionTest(context.Background(), newConnectionTestPath("node-a", member), "proxy", "tcp", "https://example.com")
	elapsed := time.Since(started)

	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	require.Less(t, elapsed, time.Second,
		"a wedged dial must be cut by the stage budget, took %s", elapsed)
	require.Equal(t, connectionTestStageFailed, stagesByID(report)[connectionTestStageDial].Status)
}

// rulePreviewingPath is a fake path that can also explain which rule decides a request.
type rulePreviewingPath struct {
	*fakeConnectionTestPath
	preview connectionTestRulePreview
	ok      bool
}

func (p *rulePreviewingPath) PreviewRouteRule(network string, domain string, port uint16) (connectionTestRulePreview, bool) {
	return p.preview, p.ok
}

// TestConnectionTestReportsTheDecidingRule pins the report when the box CAN explain the rule: the
// stage names the rule, its action and its outbound, and it runs before the dial so the explanation
// survives a failing dial.
func TestConnectionTestReportsTheDecidingRule(t *testing.T) {
	member := &fakeConnectionOutbound{
		tag: "node-a",
		behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
			return stubConn{}, nil
		},
	}
	path := &rulePreviewingPath{
		fakeConnectionTestPath: newConnectionTestPath("node-a", member),
		ok:                     true,
		preview: connectionTestRulePreview{
			Matched:    true,
			RuleIndex:  2,
			Rule:       "domain:blocked.example",
			ActionType: "route",
			Outbound:   "node-a",
		},
	}
	report := runConnectionTest(context.Background(), path, "proxy", "tcp", "https://example.com/path")

	stages := stagesByID(report)
	rulesStage := stages[connectionTestStageRules]
	require.Equal(t, connectionTestStageOK, rulesStage.Status)
	require.Equal(t, connectionTestStagePath, rulesStage.Parent)
	require.Contains(t, rulesStage.Detail, "rule[2]")
	require.Contains(t, rulesStage.Detail, "domain:blocked.example")
	require.Contains(t, rulesStage.Detail, "node-a")
	require.Equal(t, connectionTestStageOK, stages[connectionTestStageDial].Status)
}

// TestConnectionTestReportsTheRuleOverriddenAsUnknown is the "unknown stays unknown" case: a rule
// that needs a fact the preview does not have must not be reported as a decision.
func TestConnectionTestReportsTheRuleOverriddenAsUnknown(t *testing.T) {
	member := &fakeConnectionOutbound{tag: "node-a", behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		return stubConn{}, nil
	}}
	path := &rulePreviewingPath{
		fakeConnectionTestPath: newConnectionTestPath("node-a", member),
		ok:                     true,
		preview: connectionTestRulePreview{
			Unknown: "the decision depends on a process path this preview was not given",
		},
	}
	report := runConnectionTest(context.Background(), path, "proxy", "tcp", "https://example.com/path")
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageRules].Status)
	require.Contains(t, stages[connectionTestStageRules].Detail, "process path")
}

// TestConnectionTestSkipsTheOptionalRuleStage covers a path that cannot preview at all.
func TestConnectionTestSkipsTheOptionalRuleStage(t *testing.T) {
	member := &fakeConnectionOutbound{tag: "node-a", behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		return stubConn{}, nil
	}}
	report := runConnectionTest(context.Background(), newConnectionTestPath("node-a", member), "proxy", "tcp", "https://example.com/path")
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageRules].Status)
	require.Contains(t, stages[connectionTestStageRules].Detail, "cannot explain")
}

// TestConnectionTestReportsRootSemanticsAndTheDivergence pins the compatibility contract: a named
// root is what is dialled, and when the route decision would pick something ELSE the report says so
// instead of implying the rule was exercised.
func TestConnectionTestReportsRootSemanticsAndTheDivergence(t *testing.T) {
	member := &fakeConnectionOutbound{tag: "node-a", behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		return stubConn{}, nil
	}}
	path := &rulePreviewingPath{
		fakeConnectionTestPath: newConnectionTestPath("node-a", member),
		ok:                     true,
		preview: connectionTestRulePreview{
			Matched:    true,
			RuleIndex:  1,
			Rule:       "domain:example.com",
			ActionType: "route",
			Outbound:   "node-b",
		},
	}
	report := runConnectionTest(context.Background(), path, "proxy", "tcp", "https://example.com/path")

	require.Equal(t, connectionTestRoutingRoot, report.Routing)
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageOK, stages[connectionTestStageDial].Status)
	require.Contains(t, stages[connectionTestStageRules].Detail, "node-b")
	require.Contains(t, stages[connectionTestStageRules].Detail, "this test dials root node-a instead",
		"the two semantics must be distinguishable in the report itself")
	require.Contains(t, stages[connectionTestStageDial].Detail, "connected via node-a")
}

// TestConnectionTestFollowsTheRouteDecisionWhenNoRootIsGiven is the real-routing mode: an empty
// root asks "which path would this request actually take", and the dial goes through THAT outbound.
func TestConnectionTestFollowsTheRouteDecisionWhenNoRootIsGiven(t *testing.T) {
	var dialledA, dialledB atomic.Int32
	memberA := &fakeConnectionOutbound{tag: "node-a", behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		dialledA.Add(1)
		return stubConn{}, nil
	}}
	memberB := &fakeConnectionOutbound{tag: "node-b", behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		dialledB.Add(1)
		return stubConn{}, nil
	}}
	base := newConnectionTestPath("node-a", memberA)
	base.outbound = &fakeOutboundManager{members: map[string]adapter.Outbound{
		"node-a": memberA,
		"node-b": memberB,
	}}
	path := &rulePreviewingPath{
		fakeConnectionTestPath: base,
		ok:                     true,
		preview: connectionTestRulePreview{
			Matched:    true,
			RuleIndex:  1,
			Rule:       "domain:example.com",
			ActionType: "route",
			Outbound:   "node-b",
		},
	}
	report := runConnectionTest(context.Background(), path, "", "tcp", "https://example.com/path")

	require.Equal(t, connectionTestRoutingRule, report.Routing)
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStagePath].Status,
		"real-routing mode has no fixed root to validate")
	require.Equal(t, connectionTestStageOK, stages[connectionTestStageDial].Status)
	require.Contains(t, stages[connectionTestStageDial].Detail, "node-b")
	require.Equal(t, connectionTestOutcomeOK, report.Outcome)
	require.Zero(t, dialledA.Load(), "the root outbound is NOT dialled in real-routing mode")
	require.EqualValues(t, 1, dialledB.Load(), "the rule's outbound is what the request would use")
}

// TestConnectionTestUsesTheDefaultOutboundWhenNoRuleMatches covers the other half of real routing:
// nothing matched, so the final outbound is the answer.
func TestConnectionTestUsesTheDefaultOutboundWhenNoRuleMatches(t *testing.T) {
	var defaultDials atomic.Int32
	direct := &fakeConnectionOutbound{tag: "direct", behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		defaultDials.Add(1)
		return stubConn{}, nil
	}}
	base := newConnectionTestPath("node-a", nil)
	base.outbound = &fakeOutboundManager{members: map[string]adapter.Outbound{}, defaultOutbound: direct}
	path := &rulePreviewingPath{
		fakeConnectionTestPath: base,
		ok:                     true,
		preview:                connectionTestRulePreview{Matched: false},
	}
	report := runConnectionTest(context.Background(), path, "", "tcp", "https://example.com/path")

	require.Equal(t, connectionTestRoutingRule, report.Routing)
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageOK, stages[connectionTestStageDial].Status)
	require.Contains(t, stages[connectionTestStageDial].Detail, "direct")
	require.EqualValues(t, 1, defaultDials.Load())
}

// TestConnectionTestRefusesRealRoutingOnAnUnknownDecision is the honest half: a decision the preview
// cannot make must not become "dial something and call it a test".
func TestConnectionTestRefusesRealRoutingOnAnUnknownDecision(t *testing.T) {
	var dials atomic.Int32
	member := &fakeConnectionOutbound{tag: "node-a", behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		dials.Add(1)
		return stubConn{}, nil
	}}
	path := &rulePreviewingPath{
		fakeConnectionTestPath: newConnectionTestPath("node-a", member),
		ok:                     true,
		preview:                connectionTestRulePreview{Unknown: "the decision depends on a process path this preview was not given"},
	}
	report := runConnectionTest(context.Background(), path, "", "tcp", "https://example.com/path")

	stages := stagesByID(report)
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageDial].Status)
	require.Contains(t, stages[connectionTestStageDial].Detail, "unknown")
	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	require.Zero(t, dials.Load(), "nothing may be dialled when the route decision is unknown")
}
