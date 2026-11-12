package libbox

// O02 — the STABLE causal vocabulary, and the one distinction the old report could not make.
//
// A failed dial has three genuinely different outcomes and the report used to collapse them:
//
//   - the transport timed out waiting for an answer (no reply);
//   - the peer or the kernel answered "no" (refused, reset, unreachable);
//   - the diagnostic's own budget ran out and the dial was abandoned still in flight - a fact about
//     the RUNNER, from which nothing at all is known about the peer.
//
// These tests pin each one from the error the REAL dial returns, never from wording, and pin that
// every stage carries a code from the fixed set. The refused case is additionally driven through a
// real net.Dialer against a closed local port, so the classification is proven on a real pipeline
// signal rather than only on a constructed error.

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/route"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// connectionTestCodeVocabulary is every code the report may carry. A stage with a code outside this
// set is a contract break: a client cannot translate a value it has never been told about.
var connectionTestCodeVocabulary = map[string]bool{
	connectionTestCodeOK:                  true,
	connectionTestCodeSkipped:             true,
	connectionTestCodeBoundReached:        true,
	connectionTestCodeUnsupportedNetwork:  true,
	connectionTestCodeTargetInvalid:       true,
	connectionTestCodeNoExit:              true,
	connectionTestCodePathFailure:         true,
	connectionTestCodePathUnknown:         true,
	connectionTestCodeDecisionUnavailable: true,
	connectionTestCodeDecisionFailed:      true,
	connectionTestCodeDecisionTimeout:     true,
	connectionTestCodeRuleRejected:        true,
	connectionTestCodeRuleUnknown:         true,
	connectionTestCodeNoRule:              true,
	connectionTestCodeResolveFailed:       true,
	connectionTestCodeNoDNSDecision:       true,
	connectionTestCodeDNSUnknown:          true,
	connectionTestCodeDNSPreview:          true,
	connectionTestCodeDNSActual:           true,
	connectionTestCodeDialRefused:         true,
	connectionTestCodeDialNoAck:           true,
	connectionTestCodeDialUnreachable:     true,
	connectionTestCodeDialReset:           true,
	connectionTestCodeDialFailed:          true,
	connectionTestCodeDialBudgetExpire:    true,
	connectionTestCodeDialCancelled:       true,
	connectionTestCodeDialNotAttempted:    true,
}

// requireEveryStageCarriesAKnownCode is the shape every case below also asserts: no stage may be
// left without a code, and no code may be invented at a call site.
func requireEveryStageCarriesAKnownCode(t *testing.T, report connectionTestReport) {
	t.Helper()
	require.NotEmpty(t, report.Stages, "a report without stages is not a diagnostic")
	for _, stage := range report.Stages {
		require.NotEmpty(t, stage.Code, "stage %q has no code: a client cannot branch on prose", stage.ID)
		require.True(t, connectionTestCodeVocabulary[stage.Code], "stage %q carries an unknown code %q", stage.ID, stage.Code)
	}
	for _, finding := range report.Findings {
		require.NotEmpty(t, finding.Code)
	}
}

// timeoutTransportError is the error a transport-level timeout arrives as: a *net.OpError wrapping
// os.ErrDeadlineExceeded, which reports Timeout() == true. It is the same shape net.Dialer returns
// when its own deadline expires while waiting for a SYN-ACK.
func timeoutTransportError() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}
}

func TestADiagnosticTellsNoReplyFromAnExplicitRefusal(t *testing.T) {
	rootMode := func(dialError error) connectionTestReport {
		member := &fakeConnectionOutbound{
			tag: "node-a",
			behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
				return nil, dialError
			},
		}
		return runConnectionTest(context.Background(), newConnectionTestPath("node-a", member), "proxy", "tcp", "https://example.com")
	}

	t.Run("no reply is a timeout", func(t *testing.T) {
		report := rootMode(timeoutTransportError())
		require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
		dial := stagesByID(report)[connectionTestStageDial]
		require.Equal(t, connectionTestCodeDialNoAck, dial.Code,
			"a transport timeout means the request went out and nothing came back; that is no-ack, not a refusal")
		requireEveryStageCarriesAKnownCode(t, report)
	})

	t.Run("an explicit refusal keeps its own code", func(t *testing.T) {
		report := rootMode(syscall.ECONNREFUSED)
		require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
		dial := stagesByID(report)[connectionTestStageDial]
		require.Equal(t, connectionTestCodeDialRefused, dial.Code)
		requireEveryStageCarriesAKnownCode(t, report)
	})

	t.Run("an unreachable network is not a silence", func(t *testing.T) {
		report := rootMode(syscall.ENETUNREACH)
		require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
		dial := stagesByID(report)[connectionTestStageDial]
		require.Equal(t, connectionTestCodeDialUnreachable, dial.Code)
		requireEveryStageCarriesAKnownCode(t, report)
	})

	t.Run("our own budget is not an observation about the peer", func(t *testing.T) {
		previous := connectionTestStageTimeout
		connectionTestStageTimeout = 100 * time.Millisecond
		t.Cleanup(func() { connectionTestStageTimeout = previous })

		member := &fakeConnectionOutbound{
			tag: "node-a",
			behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
				// A dial that ignores its context: it is abandoned by the report, and its late
				// result must not become a verdict.
				time.Sleep(time.Second)
				return nil, errors.New("too late to matter")
			},
		}
		report := runConnectionTest(context.Background(), newConnectionTestPath("node-a", member), "proxy", "tcp", "https://example.com")
		require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
		dial := stagesByID(report)[connectionTestStageDial]
		require.Equal(t, connectionTestCodeDialBudgetExpire, dial.Code,
			"the runner stopping its own wait proves nothing about the peer and must not be reported as a refusal")
		requireEveryStageCarriesAKnownCode(t, report)
	})
}

// TestARealRefusedDialIsClassifiedFromTheDial drives the REAL net stack: a closed local port refuses
// the connection, and the code must follow the error rather than any expectation about the fixture.
func TestARealRefusedDialIsClassifiedFromTheDial(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())

	member := &fakeConnectionOutbound{
		tag: "node-a",
		behavior: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, "127.0.0.1:"+strconv.Itoa(port))
		},
	}
	report := runConnectionTest(context.Background(), newConnectionTestPath("node-a", member), "proxy", "tcp", "https://127.0.0.1/")
	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	dial := stagesByID(report)[connectionTestStageDial]
	require.Equal(t, connectionTestCodeDialRefused, dial.Code,
		"a closed port really refused the connection; the code must say refused, not failed: %s", dial.Detail)
	requireEveryStageCarriesAKnownCode(t, report)
}

// TestASuccessfulDiagnosticReportsTheRecoveryItObserved pins the recovered finding: the route layer
// reports the hand-off a group made, and the report must carry it as a finding with a code rather
// than as prose inside another stage.
func TestASuccessfulDiagnosticReportsTheRecoveryItObserved(t *testing.T) {
	path := &routeDecisionPath{
		fakeConnectionTestPath: newConnectionTestPath("", nil),
		decide: func(ctx context.Context, request route.RouteDiagnoseRequest) (route.RouteDiagnoseResult, error) {
			return route.RouteDiagnoseResult{
				Matched:          true,
				RuleIndex:        0,
				Rule:             "domain:example.com",
				ActionType:       "route",
				Outbound:         "lb",
				Dialed:           true,
				DialedOutbound:   "node-b",
				RecoveredThrough: "lb",
				RecoveredFrom:    "node-a",
				RecoveredTo:      "node-b",
				Conn:             stubConn{},
			}, nil
		},
	}
	report := runConnectionTest(context.Background(), path, "", "tcp", "https://example.com")
	require.Equal(t, connectionTestOutcomeOK, report.Outcome)

	var recovered *connectionTestFinding
	for index := range report.Findings {
		if report.Findings[index].Code == connectionTestFindingRecovered {
			recovered = &report.Findings[index]
		}
	}
	require.NotNil(t, recovered, "a hand-off observed during the dial must be reported as a finding")
	require.Equal(t, connectionTestStageDial, recovered.Stage)
	require.Contains(t, recovered.Detail, "node-a")
	require.Contains(t, recovered.Detail, "node-b")
	require.Contains(t, recovered.Detail, "lb")

	// The narrative says the same thing, with the same vocabulary.
	found := false
	for _, event := range report.Events {
		if strings.Contains(event, "recovered") && strings.Contains(event, connectionTestFindingRecovered) {
			found = true
		}
	}
	require.True(t, found, "the live narrative must carry the recovered finding: %v", report.Events)
	requireEveryStageCarriesAKnownCode(t, report)
}

// TestADiagnosticWithoutARecoveryClaimsNone: the finding is an observation, not a decoration.
func TestADiagnosticWithoutARecoveryClaimsNone(t *testing.T) {
	path := &routeDecisionPath{
		fakeConnectionTestPath: newConnectionTestPath("", nil),
		decide: func(ctx context.Context, request route.RouteDiagnoseRequest) (route.RouteDiagnoseResult, error) {
			return route.RouteDiagnoseResult{
				Matched:        true,
				RuleIndex:      0,
				Rule:           "domain:example.com",
				ActionType:     "route",
				Outbound:       "node-a",
				Dialed:         true,
				DialedOutbound: "node-a",
				Conn:           stubConn{},
			}, nil
		},
	}
	report := runConnectionTest(context.Background(), path, "", "tcp", "https://example.com")
	require.Equal(t, connectionTestOutcomeOK, report.Outcome)
	for _, finding := range report.Findings {
		require.NotEqual(t, connectionTestFindingRecovered, finding.Code,
			"nothing recovered, so claiming a recovery would be an invented fact")
	}
}

// TestTheRefusalCodeIsTheBoundAndNotAFailureOfThePath keeps the concurrency refusal distinguishable
// from a path failure: a client should be able to tell "try again" from "your proxy is broken".
func TestTheRefusalCodeIsTheBoundAndNotAFailureOfThePath(t *testing.T) {
	for index := 0; index < connectionTestMaxConcurrent; index++ {
		require.True(t, acquireConnectionTestSlot())
	}
	t.Cleanup(func() {
		for index := 0; index < connectionTestMaxConcurrent; index++ {
			releaseConnectionTestSlot()
		}
	})
	report := runConnectionTest(context.Background(), newConnectionTestPath("node-a", nil), "proxy", "tcp", "https://example.com")
	require.Equal(t, connectionTestOutcomeFailed, report.Outcome)
	require.Equal(t, connectionTestCodeBoundReached, report.Stages[0].Code)
	requireEveryStageCarriesAKnownCode(t, report)
}
