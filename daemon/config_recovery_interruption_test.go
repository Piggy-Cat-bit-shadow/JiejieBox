package daemon

// 09 — the real recovery entry must not promote a first combined commit that never reached its
// commit point, and a legacy store must keep its previous document across an interrupted upgrade.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeInterruptedCandidate(t *testing.T, directory string, candidate []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "pending.json"), candidate, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "active.json"), candidate, 0o600))
	marker, err := json.Marshal(map[string]string{"revision": configRevisionOf(string(candidate))})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(directory, "commit.candidate"), marker, 0o600))
	_, err = os.Stat(filepath.Join(directory, "commit.json"))
	require.ErrorIs(t, err, os.ErrNotExist, "the fixture must not have a commit point")
}

func TestRecoveryDoesNotPromoteAnUncommittedFirstConfiguration(t *testing.T) {
	directory := t.TempDir()
	candidate := buildFailingConfig
	writeInterruptedCandidate(t, directory, []byte(candidate))

	service := recoveryService(t, directory, true)
	require.Error(t, service.StartOrReloadService(context.Background(), anotherBuildFailingConfig, nil),
		"there is no committed configuration to recover")
	require.Nil(t, service.Instance())
	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusFailed, report.Status)
	require.Empty(t, report.ActiveRevision)
	_, err := os.Stat(filepath.Join(directory, "active.json"))
	require.ErrorIs(t, err, os.ErrNotExist, "the uncommitted candidate is removed")
}

func TestRecoveryKeepsTheLegacyDocumentAcrossAnInterruptedUpgrade(t *testing.T) {
	directory := t.TempDir()
	legacy := minimalConfig
	candidate := ruleConfig
	require.NoError(t, os.WriteFile(filepath.Join(directory, "active.json"), []byte(candidate), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "last-good.json"), []byte(legacy), 0o600))
	writeInterruptedCandidate(t, directory, []byte(candidate))

	service := recoveryService(t, directory, true)
	require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil),
		"the legacy document is a complete recovery target")
	require.NotNil(t, service.Instance())
	report := service.LastServiceChange()
	require.Equal(t, ServiceChangeStatusRecovered, report.Status)
	require.Equal(t, configRevisionOf(legacy), report.ActiveRevision)
	require.False(t, report.Retained, "nothing was running before the attempt; the recovery started the legacy document")
	active, err := os.ReadFile(filepath.Join(directory, "active.json"))
	require.NoError(t, err)
	require.Equal(t, legacy, string(active), "the legacy active is restored")
}
