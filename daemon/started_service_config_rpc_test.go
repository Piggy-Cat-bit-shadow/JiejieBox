package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// The additive configuration RPCs carry the same JSON the in-process contract exposes, so a client
// that has not regenerated the field-level messages can still display a plan, a report and a
// recovery. This pins the wire values rather than only the Go structs.
func TestConfigurationRPCsExposeTheInProcessContract(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, false)
	ctx := context.Background()

	emptyReport, err := service.GetLastServiceChange(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	require.JSONEq(t, "null", emptyReport.Json)
	emptyRecovery, err := service.GetLastConfigRecovery(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	require.JSONEq(t, "null", emptyRecovery.Json)

	require.NoError(t, service.StartOrReloadService(ctx, minimalConfig, nil))
	reportDocument, err := service.GetLastServiceChange(ctx, &emptypb.Empty{})
	require.NoError(t, err)
	var report ServiceChangeReport
	require.NoError(t, json.Unmarshal([]byte(reportDocument.Json), &report))
	require.Equal(t, ServiceChangeStatusStarted, report.Status)
	require.Equal(t, configRevisionOf(minimalConfig), report.Plan.TargetRevision)

	planDocument, err := service.PreviewServiceChange(ctx, &PreviewServiceChangeRequest{ConfigContent: minimalConfig})
	require.NoError(t, err)
	var plan ServiceChangePlan
	require.NoError(t, json.Unmarshal([]byte(planDocument.Json), &plan))
	require.Equal(t, configRevisionOf(minimalConfig), plan.TargetRevision)
	require.True(t, plan.Unchanged, "the running document is previewed as unchanged")
}
