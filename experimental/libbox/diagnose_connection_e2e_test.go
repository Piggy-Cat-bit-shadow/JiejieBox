package libbox

// P1.2 — the active diagnostic end to end against a REAL CommandServer and a REAL Box.
//
// The reviewed failure needed no fake: a route rule rewrote the target's port and the test dialled
// the original one while reporting "connected via direct (route decision)". This file drives
// CommandServer.ConnectionTest over the real route pipeline and two real local listeners, then a
// controlled failure and a recovery, so the report's claim is the connection that actually happened.

import (
	"encoding/json"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// countingListener accepts TCP connections and counts them.
type countingListener struct {
	listener net.Listener
	count    atomic.Int32
	closed   atomic.Bool
}

func newCountingListener(t *testing.T) *countingListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { listener.Close() })
	counting := &countingListener{listener: listener}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			counting.count.Add(1)
			_ = conn.Close()
		}
	}()
	return counting
}

func (l *countingListener) port() uint16 {
	return uint16(l.listener.Addr().(*net.TCPAddr).Port)
}

// startDiagnoseTestCommandServer mirrors the running-box fixture used by the provider end-to-end
// test: real CommandServer, real Box, package paths pointed at temporary directories.
func startDiagnoseTestCommandServer(t *testing.T, configContent string) *CommandServer {
	t.Helper()
	previousWorking, previousTemp := sWorkingPath, sTempPath
	previousBase := sBasePath
	previousUser, previousGroup := sUserID, sGroupID
	sWorkingPath, sTempPath, sBasePath = t.TempDir(), t.TempDir(), t.TempDir()
	sUserID, sGroupID = -1, -1
	t.Cleanup(func() {
		sWorkingPath, sTempPath = previousWorking, previousTemp
		sBasePath = previousBase
		sUserID, sGroupID = previousUser, previousGroup
	})
	server, err := NewCommandServer(nil, refreshPlatform{})
	require.NoError(t, err)
	t.Cleanup(server.Close)
	require.NoError(t, server.StartOrReloadService(configContent, &OverrideOptions{}))
	return server
}

// runDiagnoseTest calls the real gomobile entry and decodes the report.
func runDiagnoseTest(t *testing.T, server *CommandServer, root, network, target string) connectionTestReport {
	t.Helper()
	boxed, err := server.ConnectionTest(root, network, target)
	require.NoError(t, err)
	require.NotNil(t, boxed)
	var report connectionTestReport
	require.NoError(t, json.Unmarshal([]byte(boxed.Value), &report), "report: %s", boxed.Value)
	return report
}

func routeConfig(rule string) string {
	config := `{"log":{"level":"error"},"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"`
	if rule != "" {
		config += `,"rules":[` + rule + `]`
	}
	return config + `}}`
}

// diagnoseConfig builds a box with optional DNS servers and route rules.
func diagnoseConfig(dnsServers string, routeRules string) string {
	config := `{"log":{"level":"error"}`
	if dnsServers != "" {
		config += `,"dns":{"servers":` + dnsServers + `,"final":"local"}`
	}
	config += `,"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"`
	if routeRules != "" {
		config += `,"rules":[` + routeRules + `]`
	}
	return config + `}}`
}

// TestTheActiveDiagnosticDialsTheRewrittenPort is the reviewed failure, end to end.
func TestTheActiveDiagnosticDialsTheRewrittenPort(t *testing.T) {
	original := newCountingListener(t)
	rewritten := newCountingListener(t)
	server := startDiagnoseTestCommandServer(t, routeConfig(fmt.Sprintf(
		`{"port":[%d],"action":"route","outbound":"direct","override_port":%d}`,
		original.port(), rewritten.port())))

	report := runDiagnoseTest(t, server, "", "tcp", fmt.Sprintf("http://127.0.0.1:%d/path", original.port()))

	require.Equal(t, connectionTestRoutingRule, report.Routing)
	require.Equal(t, connectionTestOutcomeOK, report.Outcome)
	require.Eventually(t, func() bool { return rewritten.count.Load() == 1 }, 5*time.Second, time.Millisecond,
		"the connection must reach the port the route decided")
	require.Zero(t, original.count.Load(), "the original port must NOT receive the diagnostic connection")
	stages := stagesByID(report)
	require.Contains(t, stages[connectionTestStageResolve].Detail, fmt.Sprint(rewritten.port()))
	require.Contains(t, stages[connectionTestStageDial].Detail, fmt.Sprint(rewritten.port()))
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageDNS].Status,
		"a literal target has no DNS decision")
}

// TestTheActiveDiagnosticNamesAControlledFailureAndRecovers: the first run fails on a destination the
// route chooses (a closed port), and after the configuration no longer rewrites the request the run
// succeeds against the real listener. Both are the same entry over the real pipeline.
func TestTheActiveDiagnosticNamesAControlledFailureAndRecovers(t *testing.T) {
	original := newCountingListener(t)
	closed := newCountingListener(t)
	closedPort := closed.port()
	closed.listener.Close()
	time.Sleep(10 * time.Millisecond)

	server := startDiagnoseTestCommandServer(t, routeConfig(fmt.Sprintf(
		`{"port":[%d],"action":"route","outbound":"direct","override_port":%d}`,
		original.port(), closedPort)))

	failed := runDiagnoseTest(t, server, "", "tcp", fmt.Sprintf("http://127.0.0.1:%d/path", original.port()))
	require.Equal(t, connectionTestOutcomeFailed, failed.Outcome)
	stages := stagesByID(failed)
	require.Equal(t, connectionTestStageFailed, stages[connectionTestStageDial].Status)
	require.Contains(t, stages[connectionTestStageDial].Detail, fmt.Sprint(closedPort),
		"the failure must name the destination the route chose")
	require.Zero(t, original.count.Load())

	// The controlled recovery: the rule no longer rewrites the request, so the same entry now
	// reaches the real listener.
	require.NoError(t, server.StartOrReloadService(routeConfig(""), &OverrideOptions{}))
	recovered := runDiagnoseTest(t, server, "", "tcp", fmt.Sprintf("http://127.0.0.1:%d/path", original.port()))
	require.Equal(t, connectionTestOutcomeOK, recovered.Outcome)
	require.Eventually(t, func() bool { return original.count.Load() == 1 }, 5*time.Second, time.Millisecond,
		"the recovered run must reach the original listener")
}

// TestTheActiveDiagnosticKeepsTheRootContract: a named root tests the fixed path and says so.
func TestTheActiveDiagnosticKeepsTheRootContract(t *testing.T) {
	listener := newCountingListener(t)
	server := startDiagnoseTestCommandServer(t, routeConfig(fmt.Sprintf(
		`{"port":[%d],"action":"route","outbound":"direct","override_port":%d}`,
		listener.port(), listener.port()+1)))
	report := runDiagnoseTest(t, server, "direct", "tcp", fmt.Sprintf("http://127.0.0.1:%d/path", listener.port()))
	require.Equal(t, "root", report.Routing)
	require.Equal(t, connectionTestOutcomeOK, report.Outcome)
	require.Eventually(t, func() bool { return listener.count.Load() == 1 }, 5*time.Second, time.Millisecond,
		"root mode dials the target as written, by contract")
}

// TestTheRealDNSStageReportsThePendingDependency drives the REAL box path's DNS preview: a DNS rule
// that reads a process path cannot be decided without one, and the stage must report that dependency
// instead of naming the default server.
func TestTheRealDNSStageReportsThePendingDependency(t *testing.T) {
	config := `{"log":{"level":"error"},"dns":{"servers":[{"type":"local","tag":"local"}],"rules":[{"process_path":["/usr/bin/curl"],"action":"route","server":"local"}]},"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`
	server := startDiagnoseTestCommandServer(t, config)
	instance := server.Instance()
	require.NotNil(t, instance)
	boxInstance := instance.Box()
	require.NotNil(t, boxInstance)
	path := newBoxConnectionTestPath(boxInstance)

	preview, previewed := path.PreviewDNSRule("tcp", "race.example.org", 443)
	require.True(t, previewed, "the real box path can explain a DNS decision")
	require.NotEmpty(t, preview.Unknown, "a process-path rule is undecidable without a process fact")
	require.Contains(t, preview.Unknown, "process_path")
	require.Empty(t, preview.Server, "no server may be named for an undecided query")
}

// TestTheActiveDiagnosticRejectsALiteralAddressRule is reviewed failure 1: the rule is an
// `ip_cidr` REJECT and the target is a literal address. The diagnostic used to hand the literal to
// the rule matcher as a FQDN, so the rule never matched and the connection was made.
func TestTheActiveDiagnosticRejectsALiteralAddressRule(t *testing.T) {
	listener := newCountingListener(t)
	server := startDiagnoseTestCommandServer(t, routeConfig(`{"ip_cidr":["127.0.0.0/8"],"action":"reject"}`))

	report := runDiagnoseTest(t, server, "", "tcp", fmt.Sprintf("http://127.0.0.1:%d/path", listener.port()))

	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageOK, stages[connectionTestStageRules].Status)
	require.Contains(t, stages[connectionTestStageRules].Detail, "ip_cidr")
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageDial].Status)
	require.Contains(t, stages[connectionTestStageDial].Detail, "rejects")
	time.Sleep(100 * time.Millisecond)
	require.Zero(t, listener.count.Load(), "a rejected request must not reach the listener")
}

// TestTheActiveDiagnosticRejectsAnIPv6Literal keeps the literal branch dual-stack: the reviewed
// failure was an IPv4 literal in a FQDN field, and an IPv6 literal has the same shape.
func TestTheActiveDiagnosticRejectsAnIPv6Literal(t *testing.T) {
	server := startDiagnoseTestCommandServer(t, routeConfig(`{"ip_cidr":["::1/128"],"action":"reject"}`))

	report := runDiagnoseTest(t, server, "", "tcp", "http://[::1]:9/path")

	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	stages := stagesByID(report)
	require.Contains(t, stages[connectionTestStageRules].Detail, "ip_cidr")
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageDNS].Status)
	require.Contains(t, stages[connectionTestStageDNS].Detail, "literal address",
		"a literal target has no DNS decision to explain")
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageDial].Status)
}

// TestTheActiveDiagnosticUsesTheResolveActionsResolver is reviewed failure 2: a route `resolve`
// action names its own DNS server, and the diagnostic must dial the address THAT server returned.
func TestTheActiveDiagnosticUsesTheResolveActionsResolver(t *testing.T) {
	listener := newCountingListener(t)
	server := startDiagnoseTestCommandServer(t, diagnoseConfig(
		`[{"type":"hosts","tag":"local","predefined":{"review.test":["127.0.0.2"]}},`+
			`{"type":"hosts","tag":"forced","predefined":{"review.test":["127.0.0.1"]}}]`,
		`{"domain":["review.test"],"action":"resolve","server":"forced","strategy":"ipv4_only"}`))

	report := runDiagnoseTest(t, server, "", "tcp", fmt.Sprintf("http://review.test:%d/path", listener.port()))

	require.Equal(t, connectionTestOutcomeOK, report.Outcome)
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageOK, stages[connectionTestStageDNS].Status)
	require.Contains(t, stages[connectionTestStageDNS].Detail, "forced",
		"the report names the resolver the resolve action actually used")
	require.Contains(t, stages[connectionTestStageDNS].Detail, "127.0.0.1")
	require.Contains(t, stages[connectionTestStageResolve].Detail, "127.0.0.1")
	require.Eventually(t, func() bool { return listener.count.Load() == 1 }, 5*time.Second, time.Millisecond,
		"the connection must reach the address the named resolver returned")
}

// TestTheActiveDiagnosticMatchesRulesAgainstTheResolvedAddress keeps the post-resolve rule order:
// a rule AFTER the resolve action sees the candidates it produced.
func TestTheActiveDiagnosticMatchesRulesAgainstTheResolvedAddress(t *testing.T) {
	listener := newCountingListener(t)
	server := startDiagnoseTestCommandServer(t, diagnoseConfig(
		`[{"type":"hosts","tag":"local","predefined":{"review.test":["127.0.0.2"]}},`+
			`{"type":"hosts","tag":"forced","predefined":{"review.test":["127.0.0.1"]}}]`,
		`{"domain":["review.test"],"action":"resolve","server":"forced"},`+
			`{"ip_cidr":["127.0.0.1/32"],"action":"reject"}`))

	report := runDiagnoseTest(t, server, "", "tcp", fmt.Sprintf("http://review.test:%d/path", listener.port()))

	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	stages := stagesByID(report)
	require.Contains(t, stages[connectionTestStageRules].Detail, "rule[1]",
		"the rule after the resolve decision is the one that decides")
	require.Contains(t, stages[connectionTestStageRules].Detail, "reject")
	time.Sleep(100 * time.Millisecond)
	require.Zero(t, listener.count.Load(), "the resolved address must not be dialled after a rejecting rule matched")
}

// TestTheActiveDiagnosticReportsAProcessRuleAsUnknown keeps the honesty rule: a socketless request
// cannot supply a process fact, so a process rule ends the walk as unknown instead of being
// evaluated against an empty record and silently "not matching".
func TestTheActiveDiagnosticReportsAProcessRuleAsUnknown(t *testing.T) {
	listener := newCountingListener(t)
	server := startDiagnoseTestCommandServer(t, routeConfig(
		`{"process_path":["/usr/bin/curl"],"action":"route","outbound":"direct"}`))

	report := runDiagnoseTest(t, server, "", "tcp", fmt.Sprintf("http://127.0.0.1:%d/path", listener.port()))

	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	stages := stagesByID(report)
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageRules].Status)
	require.Contains(t, stages[connectionTestStageRules].Detail, "process facts")
	require.Equal(t, connectionTestStageSkipped, stages[connectionTestStageDial].Status)
	time.Sleep(100 * time.Millisecond)
	require.Zero(t, listener.count.Load(), "an undecidable request must not be dialled")
}

// TestTheActiveDiagnosticFallsBackWithinOneConfig is the controlled fallback: the named resolver
// answers with a dead address first and the live one second, and the SAME execution connects.
func TestTheActiveDiagnosticFallsBackWithinOneConfig(t *testing.T) {
	listener := newCountingListener(t)
	server := startDiagnoseTestCommandServer(t, diagnoseConfig(
		`[{"type":"hosts","tag":"local","predefined":{"localhost":["127.0.0.1"]}},`+
			`{"type":"hosts","tag":"forced","predefined":{"fallback.test":["127.0.0.2","127.0.0.1"]}}]`,
		`{"domain":["fallback.test"],"action":"resolve","server":"forced","strategy":"ipv4_only"}`))

	report := runDiagnoseTest(t, server, "", "tcp", fmt.Sprintf("http://fallback.test:%d/path", listener.port()))

	require.Equal(t, connectionTestOutcomeOK, report.Outcome)
	stages := stagesByID(report)
	require.Contains(t, stages[connectionTestStageResolve].Detail, "127.0.0.1")
	require.Contains(t, stages[connectionTestStageDial].Detail, "127.0.0.1")
	require.Eventually(t, func() bool { return listener.count.Load() == 1 }, 5*time.Second, time.Millisecond,
		"the second candidate is the one that answers, inside the same diagnostic execution")
}
