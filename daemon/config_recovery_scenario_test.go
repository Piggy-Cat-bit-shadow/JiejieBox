package daemon

// C05 review — the recovery report after a chain of attempts.
//
// The original reproduction has three configurations in a row:
//
//	A  starts and is saved as the last-good document;
//	B  starts and RUNS, but the disk refuses to save it;
//	C  fails to start.
//
// Recovery can only return to A, because A is what the store kept. The report must say exactly
// that: the revision that runs (A), whether the configuration that ran before the attempt (B) is
// still the one running (it is not), and the plan's answer about a restart. The entry used to
// report `retained` for every successful recovery, which is true only when the recovered document
// IS the one that was running before the attempt.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/common/configtx"

	"github.com/stretchr/testify/require"
)

// recoveryStoreWithWriteFault opens the daemon's store with one write failure injected: the active
// document of the refused revision is rejected, everything else reaches the disk. This is the one
// failure a test cannot produce portably with a real file (a directory in place of the file makes
// the read fail too, so the previous active would not survive as last-good).
func recoveryStoreWithWriteFault(t *testing.T, directory string, refused []byte) *configtx.Store {
	t.Helper()
	store, err := configtx.Open(directory, configtx.Options{
		Validator: validConfigDocument,
		WriteFile: func(path string, content []byte) error {
			if filepath.Base(path) == "active.json" && bytes.Equal(content, refused) {
				return errors.New("injected: the disk refused the active document")
			}
			return os.WriteFile(path, content, 0o644)
		},
	})
	require.NoError(t, err)
	return store
}

// installStore replaces the store the service opened with one that can inject a write failure. The
// service still drives the real transaction, the real entry and the real recovery path; only the
// low-level write is different.
func installStore(service *StartedService, store *configtx.Store) {
	service.configRecoveryAccess.Lock()
	service.configRecovery = store
	service.configRecoveryAccess.Unlock()
}

// TestARecoveryReturnsToTheSavedConfigurationWhenTheRunningOneWasNotSaved is the original
// A -> B -> C reproduction:
//
//	A was saved, B ran but could not be saved, C failed to start. The store's last-good is A and
//	its active is still A, so the recovery starts A. Before the attempt B was running; after it A
//	is, so the previous configuration was NOT retained and the report must not claim it was.
func TestARecoveryReturnsToTheSavedConfigurationWhenTheRunningOneWasNotSaved(t *testing.T) {
	directory := t.TempDir()
	store := recoveryStoreWithWriteFault(t, directory, []byte(ruleConfig))
	service := recoveryService(t, directory, true)
	installStore(service, store)

	// A: saved, and it is the document the store will keep.
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))
	require.Equal(t, minimalConfig, storedActive(t, directory))
	require.Equal(t, persistenceSaved, service.LastServiceChange().Persistence)

	// B: runs, but the disk refuses to make it the active document.
	require.NoError(t, service.StartOrReloadService(context.Background(), ruleConfig, nil))
	require.NotNil(t, service.Instance(), "B is running even though it was not saved")
	bReport := service.LastServiceChange()
	require.Equal(t, ServiceChangeStatusRestarted, bReport.Status)
	require.Equal(t, persistenceDegraded, bReport.Persistence,
		"the disk refused B, so a later failed start cannot return to it")
	require.Equal(t, minimalConfig, storedActive(t, directory),
		"the saved document is still A; that is what recovery has to work with")

	// C: fails to start. Recovery can only return to A.
	require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil))
	require.NotNil(t, service.Instance(), "recovery started the saved configuration")

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusRecovered, report.Status)
	require.Equal(t, configRevisionOf(minimalConfig), report.ActiveRevision,
		"the report names the revision that runs, which is A, not the one that failed and not B")
	require.False(t, report.Retained,
		"B was running before the attempt and A runs now: the previous configuration was not retained")
	require.True(t, report.Plan.RestartRequired)
	require.Equal(t, report.Plan.RestartRequired, report.RestartRequired,
		"the report's outer answer must agree with the plan it carries")

	info := service.LastConfigRecovery()
	require.NotNil(t, info)
	require.Equal(t, configRevisionOf(buildFailingConfig), info.FailedRevision)
	require.Equal(t, configRevisionOf(minimalConfig), info.ActiveRevision)
	require.Equal(t, minimalConfig, storedActive(t, directory),
		"recovery does not overwrite the saved document with the failed candidate")
}

// TestARecoveryBackToTheDocumentThatWasAlreadyRunningIsRetained is the other side of the same
// distinction: when the recovered document IS the one that was running, saying `retained` is
// correct. Without this pair a "retained: false" constant would pass the test above.
func TestARecoveryBackToTheDocumentThatWasAlreadyRunningIsRetained(t *testing.T) {
	service := recoveryService(t, t.TempDir(), true)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))

	require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil))

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusRecovered, report.Status)
	require.Equal(t, configRevisionOf(minimalConfig), report.ActiveRevision)
	require.True(t, report.Retained, "the document that ran before the attempt is running again")
}

// TestAFailedStartWithoutRecoveryReportsThatNothingRuns covers the state the report used to hide:
// the previous Box is closed before the new one is built, so a failed start without recovery
// leaves NO configuration running. Naming the previous revision as `activeRevision` claims a
// service that is not there.
func TestAFailedStartWithoutRecoveryReportsThatNothingRuns(t *testing.T) {
	service := recoveryService(t, t.TempDir(), false)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))
	require.NotNil(t, service.Instance())

	require.Error(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil))
	require.Nil(t, service.Instance(), "the failed start left nothing running")

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusFailed, report.Status)
	require.Empty(t, report.ActiveRevision,
		"nothing is running, so no revision may be reported as active")
	require.False(t, report.Retained)
	require.Equal(t, report.Plan.RestartRequired, report.RestartRequired)
}

// TestAnInterruptedStartReportsThatNothingRuns covers the other way an attempt can leave no
// service: the start is interrupted after the old Box was closed. Reporting the previous revision
// as active is the same lie as reporting it after a failed build.
func TestAnInterruptedStartReportsThatNothingRuns(t *testing.T) {
	service := recoveryService(t, t.TempDir(), true)
	service.beforeInstanceStart = service.interruptStart

	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil),
		"an interrupted start is not an error the caller has to handle")
	require.Nil(t, service.Instance(), "the interrupted start left nothing running")

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusInterrupted, report.Status)
	require.Empty(t, report.ActiveRevision)
	require.False(t, report.Retained)
	require.Equal(t, report.Plan.RestartRequired, report.RestartRequired)
}

// TestAReferenceWriteFailureIsReportedDegraded covers the combined commit boundary: the new Box
// runs, the configuration and its references did not reach the disk as one combination, and the
// caller must see degraded rather than saved.
func TestAReferenceWriteFailureIsReportedDegraded(t *testing.T) {
	ctx := recoveryBusinessContext(t)
	rulesPayload := binaryRuleSetForTest(t, "127.0.0.1/32")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(rulesPayload)
	}))
	t.Cleanup(server.Close)

	directory := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(directory, "references.json"), 0o755))
	service := NewStartedService(ServiceOptions{Context: ctx})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	service.ConfigureConfigRecovery(directory, true)
	profile := recoveryReferenceConfig(freeLoopbackPort(t), server.URL, "error", "1h")
	require.NoError(t, service.StartOrReloadService(context.Background(), profile, nil))
	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, persistenceDegraded, report.Persistence)
	require.Contains(t, report.PersistenceWarning, "references were not saved")
	require.NotNil(t, service.Instance(), "the configuration is running even though the combination did not commit")
}

// TestAFailedRecoveryReportsThatNothingRuns is the same state after recovery was attempted and
// failed too: both the candidate and the saved document are unusable.
func TestAFailedRecoveryReportsThatNothingRuns(t *testing.T) {
	directory := t.TempDir()
	store, err := configtx.Open(directory, configtx.Options{Validator: validConfigDocument})
	require.NoError(t, err)
	staged, err := store.Stage([]byte(buildFailingConfig))
	require.NoError(t, err)
	_, err = store.Commit(staged)
	require.NoError(t, err)

	service := recoveryService(t, directory, true)
	require.Error(t, service.StartOrReloadService(context.Background(), anotherBuildFailingConfig, nil))
	require.Nil(t, service.Instance())

	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusFailed, report.Status)
	require.Empty(t, report.ActiveRevision)
	require.False(t, report.Retained)
}
