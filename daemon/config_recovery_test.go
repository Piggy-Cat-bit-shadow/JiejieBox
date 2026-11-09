package daemon

// C05 — the last-good transaction driven through the real start path.
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

// failingConfig parses as JSON but cannot become a running Box: its inbound type is not
// registered in this harness, so construction fails - the "start failed" case recovery is for.
const failingConfig = `{"log":{"level":"error"},"inbounds":[{"type":"mixed","tag":"in","listen":"127.0.0.1","listen_port":0}]}`

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
	require.NoError(t, service.StartOrReloadService(context.Background(), failingConfig, nil),
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

	require.Error(t, service.StartOrReloadService(context.Background(), failingConfig, nil),
		"without opt-in, a failed start still fails")
	require.Nil(t, service.LastConfigRecovery())
}

func TestAFailedFirstStartHasNothingToRecover(t *testing.T) {
	directory := t.TempDir()
	service := recoveryService(t, directory, true)
	require.Error(t, service.StartOrReloadService(context.Background(), failingConfig, nil),
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
