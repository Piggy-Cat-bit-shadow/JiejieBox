package daemon

// C05 - the last-good transaction driven through the real start path.
//
// These tests use the same harness the device-axis reload test uses (a working directory, the
// registries a Box needs, no pause manager), so they exercise StartOrReloadService rather than
// a stand-in for it.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/common/configtx"

	"github.com/stretchr/testify/require"
)

// buildFailingConfig parses and then fails at start: its only rule names a rule-set the document
// never declares, which `box.New`'s rule-set wiring rejects.
//
// This is the failure recovery exists for: the start WAS attempted, so the previous Box is gone
// and the daemon would be left with nothing. It is deliberately not the "unknown inbound type"
// profile used here before - that one fails while PARSING, and the entry now refuses such a
// candidate before it closes the running Box (see service_change_test.go), so no recovery is
// involved and none should run.
const buildFailingConfig = `{"log":{"level":"error"},"route":{"rules":[{"rule_set":"missing","action":"reject"}]}}`

// anotherBuildFailingConfig fails the same way with a different document, so a recovery attempt
// from one to the other is not skipped as "the requested profile IS the stored one".
const anotherBuildFailingConfig = `{"log":{"level":"error"},"route":{"rules":[{"rule_set":"other-missing","action":"reject"}]}}`

// missingLocalRuleSetConfig names a local rule-set file that does not exist. The document parses -
// a path is a string - and the start fails when the router reads it, which is the "referenced file
// missing" failure a profile editor can produce and a reload has to survive.
const missingLocalRuleSetConfig = `{
  "log": {"level": "error"},
  "route": {
    "rule_set": [{"type": "local", "tag": "from-file", "format": "source", "path": "does-not-exist.json"}],
    "rules": [{"rule_set": "from-file", "action": "reject"}]
  }
}`

func recoveryService(t *testing.T, directory string, recoverToLastGood bool) *StartedService {
	t.Helper()
	service := NewStartedService(ServiceOptions{
		Context:                 libboxContext(t),
		ConfigRecoveryDirectory: directory,
		RecoverToLastGood:       recoverToLastGood,
	})
	t.Cleanup(service.Close)
	return service
}

func storedActive(t *testing.T, directory string) string {
	t.Helper()
	store, err := configtx.Open(directory, configtx.Options{})
	require.NoError(t, err)
	active, err := store.Active()
	require.NoError(t, err)
	return string(active)
}

func TestASuccessfulStartBecomesTheActiveConfiguration(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, false)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))
	require.Equal(t, minimalConfig, storedActive(t, directory),
		"the profile that actually started is the document a later start returns to")
	require.Nil(t, service.LastConfigRecovery(), "a normal start is not a recovery")
}

func TestAFailedStartRecoversToTheLastGoodConfiguration(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, true)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))

	// The reload fails to build; recovery starts the stored profile again.
	require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil),
		"with recovery enabled the daemon is running again, so the call succeeds")
	require.NotNil(t, service.Instance(), "a Box must be running after recovery")
	info := service.LastConfigRecovery()
	require.NotNil(t, info)
	require.NotEmpty(t, info.Error, "the recovered event keeps the failure that caused it")
	require.NotEqual(t, info.FailedRevision, info.ActiveRevision)
	require.Equal(t, minimalConfig, storedActive(t, directory), "recovery does not replace the active document")
}

func TestAFailedStartWithoutTheFlagKeepsTheError(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, false)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))

	require.Error(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil),
		"without opt-in, a failed start still fails")
	require.Nil(t, service.LastConfigRecovery())
}

func TestAFailedFirstStartHasNothingToRecover(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, true)
	require.Error(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil),
		"with no stored profile and no running Box, the failure is the answer")
	require.Nil(t, service.LastConfigRecovery())
}

func TestAStaticallyInvalidProfileNeverBecomesPending(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, true)
	require.Error(t, service.StartOrReloadService(context.Background(), `{not json`, nil))
	store, err := configtx.Open(directory, configtx.Options{})
	require.NoError(t, err)
	require.False(t, store.Pending(), "a rejected candidate must not survive as a pending file")
}

// TestARecoveryThatAlsoFailsLeavesNothingRunning covers the second failure: the requested profile
// fails AND the stored last-good one fails too. The call must fail, nothing may be left running,
// and a failed recovery must not be reported as a recovery.
func TestARecoveryThatAlsoFailsLeavesNothingRunning(t *testing.T) {
	directory := t.TempDir()
	// Seed the store with a profile that cannot start, so recovery has nothing that works.
	store, err := configtx.Open(directory, configtx.Options{})
	require.NoError(t, err)
	staged, err := store.Stage([]byte(buildFailingConfig))
	require.NoError(t, err)
	_, err = store.Commit(staged)
	require.NoError(t, err)

	service := recoveryService(t, directory, true)
	err = service.StartOrReloadService(context.Background(), anotherBuildFailingConfig, nil)
	require.Error(t, err, "both the candidate and the stored profile fail, so the call fails")
	require.Nil(t, service.Instance(), "nothing may be left running")
	require.Nil(t, service.LastConfigRecovery(), "a failed recovery is not a recovery")
}

// TestAMissingReferencedFileRecoversToTheLastGoodConfiguration covers the failure a profile edit
// produces most often: the document is valid, the file it references is gone.
func TestAMissingReferencedFileRecoversToTheLastGoodConfiguration(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, true)
	require.NoError(t, service.StartOrReloadService(context.Background(), minimalConfig, nil))

	require.NoError(t, service.StartOrReloadService(context.Background(), missingLocalRuleSetConfig, nil),
		"with recovery enabled the daemon is running again, so the call succeeds")
	require.NotNil(t, service.Instance())
	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusRecovered, report.Status)
	require.True(t, report.Retained, "the stored last-good configuration is what runs")
	require.NotEmpty(t, report.Reason, "the failure that caused the recovery is kept")
}
