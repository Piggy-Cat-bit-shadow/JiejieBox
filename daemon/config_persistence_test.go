package daemon

// C05 - committed but not persisted is a state of its own.
//
// The last-good store is how a later failed start returns to a configuration that worked. When the
// disk refuses the write, the configuration is STILL the one running - the Box was built from it and
// the user is using it - but the daemon cannot recover to it. Reporting that as plain success is
// wrong in the expensive direction: nobody learns that the recovery target is missing until the next
// failure.
//
// These tests drive the real entry with a real store and make the store fail where Windows and Unix
// agree it fails: a DIRECTORY where the active file belongs, so the write is refused.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestASavedConfigurationIsReportedAsSaved(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, false)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, persistenceSaved, report.Persistence)
	require.Empty(t, report.PersistenceWarning)
}

func TestAServiceWithoutAStoreReportsNoPersistence(t *testing.T) {
	service := startedForChange(t, minimalConfig)
	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, persistenceNotConfigured, report.Persistence,
		"the daemon was not given a store, which is not a degradation")
	require.Empty(t, report.PersistenceWarning)
}

func TestAConfigurationTheDiskRefusedIsReportedAsDegraded(t *testing.T) {
	directory := t.TempDir()
	// A directory where the active document belongs: Stage writes the pending file, Commit's
	// active write is refused, and the store reports Persisted=false with a warning.
	require.NoError(t, os.Mkdir(filepath.Join(directory, "active.json"), 0o755))

	service := recoveryService(t, directory, false)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil),
		"the configuration is running; the disk failure must not turn it into a failed start")

	require.NotNil(t, service.Instance(), "the Box is running from the configuration that was not saved")
	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, persistenceDegraded, report.Persistence,
		"committed but unsaved must be reported as committed and degraded, not as success")
	require.NotEmpty(t, report.PersistenceWarning, "the store's own message is what explains the state")
	require.Equal(t, ServiceChangeStatusStarted, report.Status)
}

func TestALiveChangeThatTheDiskRefusedIsStillReportedAsAppliedAndDegraded(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, false)
	require.NoError(t, service.StartOrReloadService(context.Background(), modeConfig("Global"), nil))
	before := service.Instance()

	// From now on the store cannot write the active document: the saved one is replaced by a
	// directory, which is a write failure on every platform this runs on.
	require.NoError(t, os.Remove(filepath.Join(directory, "active.json")))
	require.NoError(t, os.Mkdir(filepath.Join(directory, "active.json"), 0o755))

	require.NoError(t, service.StartOrReloadService(context.Background(), modeConfig("Direct"), nil))
	require.Same(t, before, service.Instance(), "the live path keeps the Box")
	require.Equal(t, "Direct", before.clashMode.Mode())

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusAppliedLive, report.Status)
	require.Equal(t, persistenceDegraded, report.Persistence)
	require.NotEmpty(t, report.PersistenceWarning)
}
