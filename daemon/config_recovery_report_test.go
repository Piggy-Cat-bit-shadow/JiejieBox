package daemon

// C05 review — two report-semantics defects found in review.
//
//  1. A configured recovery directory that could not be OPENED was reported as "no store
//     configured": the caller asked for recovery, did not get it, and the report said the same
//     thing as a daemon that never asked. It is a degradation with a reason.
//  2. A recovered report carried the plan of the document that FAILED, so a caller could not tell
//     which revision is actually running after the recovery. The report now names both: the target
//     it was asked for and the revision that runs.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAConfiguredRecoveryDirectoryThatCannotBeOpenedIsReportedDegraded(t *testing.T) {
	directory := t.TempDir()
	// A FILE where the directory belongs: the store cannot be created, which is the "configured
	// but unusable" case, not the "not configured" one.
	blocked := filepath.Join(directory, "not-a-directory")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))

	service := NewStartedService(ServiceOptions{Context: libboxContext(t), ConfigRecoveryDirectory: blocked})
	t.Cleanup(service.Close)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil),
		"a broken recovery directory must not fail the start itself")

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, persistenceDegraded, report.Persistence)
	require.Contains(t, report.PersistenceWarning, "could not be opened",
		"the report must say why recovery is unavailable, not pretend no store was configured")
}

func TestARecoveryReportNamesTheRevisionThatActuallyRuns(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, true)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))

	require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil))

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusRecovered, report.Status)
	require.True(t, report.Retained)
	require.Equal(t, configRevisionOf(buildFailingConfig), report.Plan.TargetRevision,
		"the plan names what was asked for")
	require.Equal(t, configRevisionOf(minimalConfig), report.ActiveRevision,
		"the report must name the document that actually runs, not the one that failed")
	require.NotEqual(t, report.Plan.TargetRevision, report.ActiveRevision)
}
