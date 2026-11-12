package adapter

// The protocol-depth record's own contract: bounded, copied on read, nil-safe, and honest about a
// truncated record. These are the properties every instrumented layer relies on, so they are tested
// once here instead of in each of them.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTheStageRecordIsBoundedAndSaysSo(t *testing.T) {
	record := NewDiagnoseStages()
	for index := 0; index < maxDiagnoseStages+4; index++ {
		record.Record(DiagnoseStage{Stage: DiagnoseStageTLS, Status: DiagnoseStageStatusOK, Code: DiagnoseCodeTLSOK})
	}
	result := record.Result()
	require.Len(t, result.Stages, maxDiagnoseStages)
	require.True(t, result.Truncated, "a full record must SAY it is full instead of silently dropping the tail")
}

func TestTheRecordIsReadAsACopy(t *testing.T) {
	record := NewDiagnoseStages()
	record.Record(DiagnoseStage{Stage: DiagnoseStageTLS, Status: DiagnoseStageStatusOK, Code: DiagnoseCodeTLSOK})
	first := record.Result()
	record.Record(DiagnoseStage{Stage: DiagnoseStageHTTP, Status: DiagnoseStageStatusOK, Code: DiagnoseCodeHTTPTunnelOK})
	require.Len(t, first.Stages, 1, "a report that was already read cannot grow under the reader")
	require.Len(t, record.Result().Stages, 2)
}

func TestAnUnobservedDialRecordsNothing(t *testing.T) {
	// No recorder: the call is a no-op, not a panic.
	RecordDiagnoseStage(context.Background(), DiagnoseStage{Stage: DiagnoseStageTLS})
	// A nil context is what a caller with no request context passes.
	RecordDiagnoseStage(nil, DiagnoseStage{Stage: DiagnoseStageTLS})
	// A nil record records nothing.
	var absent *DiagnoseStages
	absent.Record(DiagnoseStage{Stage: DiagnoseStageTLS})
	require.Empty(t, absent.Result().Stages)
	require.Empty(t, (&DiagnoseStages{}).Result().Stages)
}

func TestTheRecorderIsReachedThroughTheContextOnly(t *testing.T) {
	record := NewDiagnoseStages()
	ctx := WithDiagnoseStages(context.Background(), record)
	require.Same(t, record, DiagnoseStagesFromContext(ctx))

	// A caller cannot fabricate a recorder with a string key, and a stray value is not mistaken for
	// one.
	fabricated := context.WithValue(context.Background(), "diagnose-stages", record)
	require.Nil(t, DiagnoseStagesFromContext(fabricated))
	require.Nil(t, DiagnoseStagesFromContext(context.Background()))

	// A nil record leaves the context untouched, so a caller that decided not to observe does not
	// have to special-case the install.
	require.Equal(t, context.Background(), WithDiagnoseStages(context.Background(), nil))
}

func TestTheStreamStageIsTheAttemptsOutcome(t *testing.T) {
	started := time.Now().Add(-2 * time.Millisecond)
	ok := DiagnoseStreamStage("v2ray http", started, nil)
	require.Equal(t, DiagnoseStageStream, ok.Stage)
	require.Equal(t, DiagnoseStageStatusOK, ok.Status)
	require.Equal(t, DiagnoseCodeStreamOK, ok.Code)
	require.GreaterOrEqual(t, ok.DurationMs, int64(0))

	cancelled := DiagnoseStreamStage("v2ray websocket", started, context.Canceled)
	require.Equal(t, DiagnoseStageStatusFailed, cancelled.Status)
	require.Equal(t, DiagnoseCodeStreamCancelled, cancelled.Code,
		"a caller giving up is not a transport verdict")

	failed := DiagnoseStreamStage("v2ray websocket", started, errors.New("handshake refused"))
	require.Equal(t, DiagnoseCodeStreamFailed, failed.Code)
	require.Equal(t, "handshake refused", failed.Detail)
}

func TestAStageWithoutALayerIsNotRecorded(t *testing.T) {
	record := NewDiagnoseStages()
	record.Record(DiagnoseStage{Code: DiagnoseCodeTLSOK})
	require.Empty(t, record.Result().Stages, "a stage with no layer would be unattributable in the report")
}
