package daemon

// C06 - the apply report belongs to the attempt that produced it.
//
// Why this exists: "apply, then read `GetLastServiceChange`" is not a receipt. The report a later
// read returns is about the LAST attempt, whoever made it, and two of the outcomes a caller must
// tell apart - interrupted and recovered - return no error at all. A caller that only had the error
// could not distinguish "my document is running" from "the daemon recovered to something else"
// from "nothing is running".
//
// These tests drive the real entry (`ApplyServiceChange`) and assert three things per outcome:
// the report is returned by the call, it describes THAT attempt rather than the last one, and the
// embedder-facing document says the same thing.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnApplyReturnsTheReportItProduced(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)

	report, err := service.ApplyServiceChange(context.Background(), minimalConfig, nil)
	require.NoError(t, err)
	require.NotNil(t, report, "the first start must describe itself")
	require.Equal(t, ServiceChangeStatusStarted, report.Status)
	require.False(t, report.Plan.Started, "there was no running Box to compare against")
	require.Equal(t, configRevisionOf(minimalConfig), report.ActiveRevision)
	require.Equal(t, configRevisionOf(minimalConfig), report.Plan.TargetRevision)
	require.False(t, report.Time.IsZero(), "a report without a time cannot be ordered against anything")

	outcome := DescribeServiceChangeOutcome(report, err)
	require.True(t, outcome.OK, "the requested document is the running one")
	require.Equal(t, ServiceChangeOutcomeVersion, outcome.Version)
	require.Empty(t, outcome.Error)
	require.Equal(t, string(ServiceChangeStatusStarted), outcome.Status)
	require.Same(t, report, outcome.Report)
}

// TestTwoAppliesKeepTheirOwnReports is the whole point: the second attempt must not rewrite the
// first one's answer. `LastServiceChange` is a pointer to the latest report, and if the entry
// returned that pointer it would be a live view of "whatever is newest" rather than a receipt.
func TestTwoAppliesKeepTheirOwnReports(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)

	first, err := service.ApplyServiceChange(context.Background(), minimalConfig, nil)
	require.NoError(t, err)
	second, err := service.ApplyServiceChange(context.Background(), ruleConfig, nil)
	require.NoError(t, err)

	require.NotSame(t, first, second)
	require.Equal(t, configRevisionOf(ruleConfig), second.ActiveRevision)
	require.Equal(t, configRevisionOf(minimalConfig), second.Plan.CurrentRevision,
		"the second attempt compares against the first one's document")
	require.Equal(t, configRevisionOf(minimalConfig), first.ActiveRevision,
		"the first report still describes the first attempt")
	require.Equal(t, ServiceChangeStatusStarted, first.Status)
	require.Same(t, second, service.LastServiceChange(), "the store still names the latest report")
}

func TestARefusedCandidateReturnsItsReportWithTheError(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)
	_, err := service.ApplyServiceChange(context.Background(), minimalConfig, nil)
	require.NoError(t, err)

	// Not a configuration at all: refused while parsing, before the running Box is touched.
	report, err := service.ApplyServiceChange(context.Background(), `{"log":`, nil)
	require.Error(t, err, "a candidate the kernel cannot parse is still an error to the caller")
	require.NotNil(t, report, "the refusal must describe what it left running")
	require.Equal(t, ServiceChangeStatusFailed, report.Status)
	require.True(t, report.Retained, "the previous document is still running")
	require.Equal(t, configRevisionOf(minimalConfig), report.ActiveRevision)
	require.NotEmpty(t, report.Reason)

	outcome := DescribeServiceChangeOutcome(report, err)
	require.False(t, outcome.OK)
	require.Equal(t, err.Error(), outcome.Error)
	require.Equal(t, string(ServiceChangeStatusFailed), outcome.Status)
}

// TestARecoveredStartIsNotSuccessAndReturnsNoError pins the distinction the error alone cannot
// express: the daemon is running, the call succeeded, and the document the caller asked for is NOT
// what runs.
func TestARecoveredStartIsNotSuccessAndReturnsNoError(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, true)
	_, err := service.ApplyServiceChange(context.Background(), minimalConfig, nil)
	require.NoError(t, err)

	report, err := service.ApplyServiceChange(context.Background(), buildFailingConfig, nil)
	require.NoError(t, err, "with recovery enabled the daemon is running again, so the call succeeds")
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusRecovered, report.Status)
	require.Equal(t, configRevisionOf(minimalConfig), report.ActiveRevision,
		"the recovered document is the one that runs, not the one that was requested")
	require.NotEqual(t, configRevisionOf(buildFailingConfig), report.ActiveRevision)

	outcome := DescribeServiceChangeOutcome(report, err)
	require.False(t, outcome.OK, "the requested document is not the running one")
	require.Empty(t, outcome.Error, "the call itself did not fail - the status says what happened")
	require.Equal(t, string(ServiceChangeStatusRecovered), outcome.Status)
}

func TestAnInterruptedStartIsNotSuccessAndNamesNoActiveRevision(t *testing.T) {
	service := recoveryService(t, t.TempDir(), true)
	service.beforeInstanceStart = service.interruptStart

	report, err := service.ApplyServiceChange(context.Background(), minimalConfig, nil)
	require.NoError(t, err, "an interrupted start is not an error the caller has to handle")
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusInterrupted, report.Status)
	require.Empty(t, report.ActiveRevision, "nothing is running, so no revision is active")

	outcome := DescribeServiceChangeOutcome(report, err)
	require.False(t, outcome.OK)
	require.Empty(t, outcome.Error)
	require.Equal(t, string(ServiceChangeStatusInterrupted), outcome.Status)
}

// TestTheOutcomeDocumentIsWhatAnEmbedderDecodes checks the wire shape: the bridge cannot return a
// document and an error at once, so the failure travels inside the document and this is the contract
// a client decodes.
func TestTheOutcomeDocumentIsWhatAnEmbedderDecodes(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)
	report, err := service.ApplyServiceChange(context.Background(), minimalConfig, nil)
	require.NoError(t, err)

	encoded := EncodeServiceChangeOutcome(DescribeServiceChangeOutcome(report, err))
	var decoded struct {
		Version int    `json:"version"`
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Status  string `json:"status"`
		Report  *struct {
			Status         string `json:"status"`
			ActiveRevision string `json:"activeRevision"`
			Plan           struct {
				TargetRevision string `json:"targetRevision"`
			} `json:"plan"`
		} `json:"report"`
	}
	require.NoError(t, json.Unmarshal([]byte(encoded), &decoded), encoded)
	require.Equal(t, ServiceChangeOutcomeVersion, decoded.Version)
	require.True(t, decoded.OK)
	require.Empty(t, decoded.Error)
	require.Equal(t, string(ServiceChangeStatusStarted), decoded.Status)
	require.NotNil(t, decoded.Report)
	require.Equal(t, configRevisionOf(minimalConfig), decoded.Report.ActiveRevision)
	require.Equal(t, configRevisionOf(minimalConfig), decoded.Report.Plan.TargetRevision)
}

// TestAnOutcomeWithoutAReportIsNotSuccess covers the only path that reports nothing: the service was
// already closed, so no attempt was made and no report was written.
func TestAnOutcomeWithoutAReportIsNotSuccess(t *testing.T) {
	outcome := DescribeServiceChangeOutcome(nil, nil)
	require.False(t, outcome.OK, "no report is not evidence that the requested document runs")
	require.Empty(t, outcome.Status)
	require.Contains(t, EncodeServiceChangeOutcome(outcome), `"ok":false`)
}
